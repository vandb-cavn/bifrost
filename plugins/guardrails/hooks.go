package guardrails

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// HTTPTransportPreHook parses per-request guardrail profile attachment from headers.
func (p *GuardrailsPlugin) HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	if header := req.CaseInsensitiveHeaderLookup("x-bf-guardrail-ids"); header != "" {
		ids := splitTrim(header, ",")
		ctx.SetValue(guardrailInputProfilesKey, ids)
		ctx.SetValue(guardrailOutputProfilesKey, ids)
	}
	return nil, nil
}

// HTTPTransportPostHook sets HTTP 246 when a warn-rule fired.
func (p *GuardrailsPlugin) HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
	if v := ctx.Value(guardrailWarnedKey); v != nil {
		if warned, ok := v.(bool); ok && warned {
			resp.StatusCode = 246
		}
	}
	return nil
}

// HTTPTransportStreamChunkHook passes chunks through unchanged.
func (p *GuardrailsPlugin) HTTPTransportStreamChunkHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return chunk, nil
}

// PreLLMHook evaluates input-scoped rules.
func (p *GuardrailsPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	vkID, teamID := getVKAndTeamFromContext(ctx)
	rules := p.cache.getInputRules(vkID, teamID)

	msgs := extractMessages(req)
	ctx.SetValue(guardrailRequestMessagesKey, msgs)
	ctx.SetValue(guardrailRequestModelKey, modelFromRequest(req))

	celVars := buildInputCELVars(req)
	content := extractInputContent(req)
	extra := extraProfileIDsFromContext(ctx, false)

	for _, cr := range rules {
		sc, err := p.evaluateRule(ctx, cr, celVars, content, false, extra)
		if err != nil {
			p.logger.Warn("guardrails: rule %q eval error: %v", cr.rule.ID, err)
			continue
		}
		if sc != nil {
			return req, sc, nil
		}
	}
	return req, nil, nil
}

// PostLLMHook evaluates output-scoped rules.
func (p *GuardrailsPlugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	vkID, teamID := getVKAndTeamFromContext(ctx)
	rules := p.cache.getOutputRules(vkID, teamID)
	if len(rules) == 0 || resp == nil {
		return resp, bifrostErr, nil
	}

	content, finishReason := extractOutputContent(resp)
	celVars := buildOutputCELVars(ctx, resp, content, finishReason)
	extra := extraProfileIDsFromContext(ctx, true)

	for _, cr := range rules {
		sc, err := p.evaluateRule(ctx, cr, celVars, content, true, extra)
		if err != nil {
			p.logger.Warn("guardrails: output rule %q eval error: %v", cr.rule.ID, err)
			continue
		}
		if sc != nil && sc.Error != nil {
			return nil, sc.Error, nil
		}
	}
	return resp, bifrostErr, nil
}

func extraProfileIDsFromContext(ctx *schemas.BifrostContext, isOutput bool) []string {
	key := guardrailInputProfilesKey
	if isOutput {
		key = guardrailOutputProfilesKey
	}
	v := ctx.Value(key)
	if v == nil {
		return nil
	}
	ids, _ := v.([]string)
	return ids
}

func (p *GuardrailsPlugin) evaluateRule(ctx *schemas.BifrostContext, cr *cachedRule, celVars map[string]interface{}, content string, isOutput bool, extraProfileIDs []string) (*schemas.LLMPluginShortCircuit, error) {
	rule := cr.rule

	if rule.SamplingRate < 100 {
		if rand.IntN(100) >= rule.SamplingRate {
			return nil, nil
		}
	}

	triggered, err := evalProgram(cr.program, celVars)
	if err != nil {
		return nil, fmt.Errorf("CEL eval: %w", err)
	}
	if !triggered {
		return nil, nil
	}

	if len(rule.Profiles) == 0 && len(extraProfileIDs) == 0 {
		p.logger.Info("[guardrails] rule %q (%s) triggered — action=%s (CEL only)", rule.Name, rule.ID, rule.Action)
		return p.applyAction(ctx, rule, "", isOutput), nil
	}

	violated, reason := p.evaluateProfiles(ctx, rule, content, isOutput, extraProfileIDs)
	if violated {
		p.logger.Info("[guardrails] rule %q (%s) violated — reason=%q", rule.Name, rule.ID, reason)
		return p.applyAction(ctx, rule, reason, isOutput), nil
	}
	// CEL triggered but no profile violation — warn-action rules still need to apply
	// (block rules are suppressed when profiles clear the content).
	if rule.Action == "warn" {
		return p.applyAction(ctx, rule, "", isOutput), nil
	}
	return nil, nil
}

const defaultProfileTimeoutMs = 10000

// resolveProfileTimeout returns the effective timeout for a single profile call.
// It is the minimum of the rule-level timeout and the profile-level timeout,
// so the profile's global cap can never be exceeded by an individual rule.
func (p *GuardrailsPlugin) resolveProfileTimeout(pid string, ruleTimeoutMs int) time.Duration {
	profileTimeoutMs := defaultProfileTimeoutMs
	if profile := p.cache.getProfile(pid); profile != nil && profile.TimeoutMs > 0 {
		profileTimeoutMs = profile.TimeoutMs
	}

	effective := profileTimeoutMs
	if ruleTimeoutMs > 0 && ruleTimeoutMs < effective {
		effective = ruleTimeoutMs
	}
	return time.Duration(effective) * time.Millisecond
}

func (p *GuardrailsPlugin) evaluateProfiles(ctx *schemas.BifrostContext, rule *configstoreTables.TableGuardrailRule, content string, isOutput bool, extraProfileIDs []string) (bool, string) {
	seen := make(map[string]struct{})
	var ordered []string
	for _, pr := range rule.Profiles {
		if !pr.Enabled {
			continue
		}
		if _, ok := seen[pr.ID]; ok {
			continue
		}
		seen[pr.ID] = struct{}{}
		ordered = append(ordered, pr.ID)
	}
	for _, id := range extraProfileIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}

	for _, pid := range ordered {
		client, ok := p.clients[pid]
		if !ok {
			p.logger.Warn("guardrails: no client for profile %q", pid)
			if !rule.FailOpen {
				return true, "profile client not found (fail-closed)"
			}
			continue
		}

		// Effective timeout = min(rule.TimeoutMs, profile.TimeoutMs) so the
		// profile's global cap is never exceeded by a per-rule override.
		timeout := p.resolveProfileTimeout(pid, rule.TimeoutMs)
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining < timeout {
				timeout = remaining
			}
		}
		if timeout < 0 {
			timeout = 0
		}
		profileCtx, cancel := context.WithTimeout(ctx, timeout)

		var violated bool
		var reason string
		var err error
		if isOutput {
			if ma, ok := client.(*modelArmorClient); ok {
				violated, reason, err = ma.EvaluateResponse(profileCtx, content)
			} else {
				violated, reason, err = client.Evaluate(profileCtx, content)
			}
		} else {
			violated, reason, err = client.Evaluate(profileCtx, content)
		}
		cancel()

		if err != nil {
			p.logger.Warn("guardrails: profile %q eval error: %v — failOpen=%v", pid, err, rule.FailOpen)
			if !rule.FailOpen {
				return true, fmt.Sprintf("profile error (fail-closed): %v", err)
			}
			continue
		}
		if violated {
			return true, reason
		}
	}
	return false, ""
}

func (p *GuardrailsPlugin) applyAction(ctx *schemas.BifrostContext, rule *configstoreTables.TableGuardrailRule, profileReason string, isOutput bool) *schemas.LLMPluginShortCircuit {
	phase := "input"
	if isOutput {
		phase = "output"
	}
	switch rule.Action {
	case "block":
		msg := rule.BlockMessage
		if msg == "" {
			msg = "Request blocked by guardrail policy"
		}
		if profileReason != "" {
			msg = profileReason
		}
		p.logger.Info("[guardrails] rule %q (%s) triggered on %s — action=block", rule.Name, rule.ID, phase)
		return &schemas.LLMPluginShortCircuit{
			Error: &schemas.BifrostError{
				StatusCode:     schemas.Ptr(446),
				IsBifrostError: true,
				AllowFallbacks: schemas.Ptr(false),
				Error: &schemas.ErrorField{
					Message: msg,
					Type:    schemas.Ptr("guardrail_violation"),
				},
			},
		}
	case "warn":
		p.logger.Info("[guardrails] rule %q (%s) triggered on %s — action=warn", rule.Name, rule.ID, phase)
		ctx.SetValue(guardrailWarnedKey, true)
		return nil
	}
	return nil
}

func buildInputCELVars(req *schemas.BifrostRequest) map[string]interface{} {
	return map[string]interface{}{
		"request": map[string]interface{}{
			"messages": extractMessages(req),
			"model":    modelFromRequest(req),
		},
		"output": map[string]interface{}{},
	}
}

func modelFromRequest(req *schemas.BifrostRequest) string {
	if req == nil || req.ChatRequest == nil {
		return ""
	}
	return req.ChatRequest.Model
}

func buildOutputCELVars(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, content, finishReason string) map[string]interface{} {
	var reqMsgs interface{} = []interface{}{}
	if v := ctx.Value(guardrailRequestMessagesKey); v != nil {
		if msgs, ok := v.([]interface{}); ok && len(msgs) > 0 {
			reqMsgs = msgs
		}
	}
	model := ""
	if v := ctx.Value(guardrailRequestModelKey); v != nil {
		model, _ = v.(string)
	}
	if resp != nil && resp.ChatResponse != nil && resp.ChatResponse.Model != "" {
		model = resp.ChatResponse.Model
	}
	return map[string]interface{}{
		"request": map[string]interface{}{
			"messages": reqMsgs,
			"model":    model,
		},
		"output": map[string]interface{}{
			"content":       content,
			"finish_reason": finishReason,
		},
	}
}

func extractMessages(req *schemas.BifrostRequest) []interface{} {
	if req == nil || req.ChatRequest == nil {
		return nil
	}
	var msgs []interface{}
	for _, m := range req.ChatRequest.Input {
		content := ""
		if m.Content != nil && m.Content.ContentStr != nil {
			content = *m.Content.ContentStr
		}
		msgs = append(msgs, map[string]interface{}{
			"role":    string(m.Role),
			"content": content,
		})
	}
	return msgs
}

func extractInputContent(req *schemas.BifrostRequest) string {
	if req == nil || req.ChatRequest == nil {
		return ""
	}
	var parts []string
	for _, m := range req.ChatRequest.Input {
		if m.Content != nil && m.Content.ContentStr != nil {
			parts = append(parts, *m.Content.ContentStr)
		}
	}
	return strings.Join(parts, "\n")
}

func extractOutputContent(resp *schemas.BifrostResponse) (content, finishReason string) {
	if resp == nil || resp.ChatResponse == nil || len(resp.ChatResponse.Choices) == 0 {
		return "", ""
	}
	ch := resp.ChatResponse.Choices[0]
	if ch.ChatNonStreamResponseChoice != nil && ch.ChatNonStreamResponseChoice.Message != nil {
		msg := ch.ChatNonStreamResponseChoice.Message
		if msg.Content != nil && msg.Content.ContentStr != nil {
			content = *msg.Content.ContentStr
		}
	}
	if ch.FinishReason != nil {
		finishReason = *ch.FinishReason
	}
	return
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}
