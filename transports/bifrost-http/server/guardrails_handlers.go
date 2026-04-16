package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	guardrailsplugin "github.com/maximhq/bifrost/plugins/guardrails"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

func (s *BifrostHTTPServer) registerGuardrailsRoutes(middlewares ...schemas.BifrostHTTPMiddleware) {
	if s.Config == nil || s.Config.ConfigStore == nil {
		return
	}
	r := s.Router
	r.GET("/api/guardrails/rules", lib.ChainMiddlewares(s.handleListGuardrailRules, middlewares...))
	r.POST("/api/guardrails/rules", lib.ChainMiddlewares(s.handleCreateGuardrailRule, middlewares...))
	r.POST("/api/guardrails/rules/validate", lib.ChainMiddlewares(s.handleValidateGuardrailRule, middlewares...))
	r.GET("/api/guardrails/rules/{id}", lib.ChainMiddlewares(s.handleGetGuardrailRule, middlewares...))
	r.PUT("/api/guardrails/rules/{id}", lib.ChainMiddlewares(s.handleUpdateGuardrailRule, middlewares...))
	r.DELETE("/api/guardrails/rules/{id}", lib.ChainMiddlewares(s.handleDeleteGuardrailRule, middlewares...))

	r.GET("/api/guardrails/profiles", lib.ChainMiddlewares(s.handleListGuardrailProfiles, middlewares...))
	r.POST("/api/guardrails/profiles", lib.ChainMiddlewares(s.handleCreateGuardrailProfile, middlewares...))
	r.GET("/api/guardrails/profiles/{id}", lib.ChainMiddlewares(s.handleGetGuardrailProfile, middlewares...))
	r.PUT("/api/guardrails/profiles/{id}", lib.ChainMiddlewares(s.handleUpdateGuardrailProfile, middlewares...))
	r.DELETE("/api/guardrails/profiles/{id}", lib.ChainMiddlewares(s.handleDeleteGuardrailProfile, middlewares...))

	r.POST("/api/guardrails/rules/{id}/profiles/{profile_id}", lib.ChainMiddlewares(s.handleLinkGuardrailProfile, middlewares...))
	r.DELETE("/api/guardrails/rules/{id}/profiles/{profile_id}", lib.ChainMiddlewares(s.handleUnlinkGuardrailProfile, middlewares...))
}

func (s *BifrostHTTPServer) handleListGuardrailRules(ctx *fasthttp.RequestCtx) {
	rules, err := s.Config.ConfigStore.GetGuardrailRules(context.Background())
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	handlers.SendJSONWithStatus(ctx, rules, http.StatusOK)
}

func (s *BifrostHTTPServer) handleGetGuardrailRule(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	rule, err := s.Config.ConfigStore.GetGuardrailRuleByID(context.Background(), id)
	if err != nil {
		handlers.SendError(ctx, http.StatusNotFound, err.Error())
		return
	}
	handlers.SendJSONWithStatus(ctx, rule, http.StatusOK)
}

func (s *BifrostHTTPServer) handleCreateGuardrailRule(ctx *fasthttp.RequestCtx) {
	var rule tables.TableGuardrailRule
	if err := json.Unmarshal(ctx.PostBody(), &rule); err != nil {
		handlers.SendError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	if rule.ID == "" {
		rule.ID = uuid.New().String()
	}
	now := time.Now()
	rule.CreatedAt = now
	rule.UpdatedAt = now

	if err := s.Config.ConfigStore.CreateGuardrailRule(context.Background(), &rule); err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	handlers.SendJSONWithStatus(ctx, rule, http.StatusCreated)
}

func (s *BifrostHTTPServer) handleUpdateGuardrailRule(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	var rule tables.TableGuardrailRule
	if err := json.Unmarshal(ctx.PostBody(), &rule); err != nil {
		handlers.SendError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	rule.ID = id
	rule.UpdatedAt = time.Now()

	if err := s.Config.ConfigStore.UpdateGuardrailRule(context.Background(), &rule); err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	handlers.SendJSONWithStatus(ctx, rule, http.StatusOK)
}

func (s *BifrostHTTPServer) handleDeleteGuardrailRule(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if err := s.Config.ConfigStore.DeleteGuardrailRule(context.Background(), id); err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

func (s *BifrostHTTPServer) handleListGuardrailProfiles(ctx *fasthttp.RequestCtx) {
	profiles, err := s.Config.ConfigStore.GetGuardrailProfiles(context.Background())
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]guardrailProfileResponse, 0, len(profiles))
	for _, profile := range profiles {
		mapped, err := toGuardrailProfileResponse(profile)
		if err != nil {
			handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
		resp = append(resp, mapped)
	}
	handlers.SendJSONWithStatus(ctx, resp, http.StatusOK)
}

type createGuardrailProfileRequest struct {
	Name         string                 `json:"name"`
	ProviderName string                 `json:"provider_name"`
	Enabled      bool                   `json:"enabled"`
	Config       map[string]interface{} `json:"config"`
}

type updateGuardrailProfileRequest struct {
	Name         *string                 `json:"name,omitempty"`
	ProviderName *string                 `json:"provider_name,omitempty"`
	Enabled      *bool                   `json:"enabled,omitempty"`
	Config       *map[string]interface{} `json:"config,omitempty"`
}

func guardrailProfileConfigJSON(config map[string]interface{}) (string, error) {
	if config == nil {
		return "", fmt.Errorf("config is required")
	}
	if len(config) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal guardrail profile config: %w", err)
	}
	return string(data), nil
}

func (s *BifrostHTTPServer) handleGetGuardrailProfile(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	profile, err := s.Config.ConfigStore.GetGuardrailProfileByID(context.Background(), id)
	if err != nil {
		handlers.SendError(ctx, http.StatusNotFound, err.Error())
		return
	}
	resp, err := toGuardrailProfileResponse(profile)
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	handlers.SendJSONWithStatus(ctx, resp, http.StatusOK)
}

func (s *BifrostHTTPServer) handleCreateGuardrailProfile(ctx *fasthttp.RequestCtx) {
	var req createGuardrailProfileRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		handlers.SendError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	configJSON, err := guardrailProfileConfigJSON(req.Config)
	if err != nil {
		handlers.SendError(ctx, http.StatusBadRequest, err.Error())
		return
	}

	profile := tables.TableGuardrailProfile{
		ID:               uuid.New().String(),
		Name:             req.Name,
		ProviderName:     req.ProviderName,
		Enabled:          req.Enabled,
		ConfigJSON:       configJSON,
		EncryptionStatus: tables.EncryptionStatusPlainText,
	}
	now := time.Now()
	profile.CreatedAt = now
	profile.UpdatedAt = now

	if err := s.Config.ConfigStore.CreateGuardrailProfile(context.Background(), &profile); err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	created, err := s.Config.ConfigStore.GetGuardrailProfileByID(context.Background(), profile.ID)
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	resp, err := toGuardrailProfileResponse(created)
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	handlers.SendJSONWithStatus(ctx, resp, http.StatusCreated)
}

func (s *BifrostHTTPServer) handleUpdateGuardrailProfile(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	var req updateGuardrailProfileRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		handlers.SendError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	profile, err := s.Config.ConfigStore.GetGuardrailProfileByID(context.Background(), id)
	if err != nil {
		handlers.SendError(ctx, http.StatusNotFound, err.Error())
		return
	}
	if req.Name != nil {
		profile.Name = *req.Name
	}
	if req.ProviderName != nil {
		profile.ProviderName = *req.ProviderName
	}
	if req.Enabled != nil {
		profile.Enabled = *req.Enabled
	}
	if req.Config != nil {
		configJSON, err := guardrailProfileConfigJSON(*req.Config)
		if err != nil {
			handlers.SendError(ctx, http.StatusBadRequest, err.Error())
			return
		}
		profile.ConfigJSON = configJSON
		profile.EncryptionStatus = tables.EncryptionStatusPlainText
	}
	profile.UpdatedAt = time.Now()

	if err := s.Config.ConfigStore.UpdateGuardrailProfile(context.Background(), profile); err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Config.ConfigStore.GetGuardrailProfileByID(context.Background(), id)
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	resp, err := toGuardrailProfileResponse(updated)
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	handlers.SendJSONWithStatus(ctx, resp, http.StatusOK)
}

func (s *BifrostHTTPServer) handleDeleteGuardrailProfile(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if err := s.Config.ConfigStore.DeleteGuardrailProfile(context.Background(), id); err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

func (s *BifrostHTTPServer) handleLinkGuardrailProfile(ctx *fasthttp.RequestCtx) {
	ruleID := ctx.UserValue("id").(string)
	profileID := ctx.UserValue("profile_id").(string)
	if err := s.Config.ConfigStore.LinkGuardrailProfile(context.Background(), ruleID, profileID); err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

func (s *BifrostHTTPServer) handleUnlinkGuardrailProfile(ctx *fasthttp.RequestCtx) {
	ruleID := ctx.UserValue("id").(string)
	profileID := ctx.UserValue("profile_id").(string)
	if err := s.Config.ConfigStore.UnlinkGuardrailProfile(context.Background(), ruleID, profileID); err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

type validateRuleRequest struct {
	CelExpression string                 `json:"cel_expression"`
	Sample        map[string]interface{} `json:"sample"`
}

type validateRuleResponse struct {
	Valid  bool    `json:"valid"`
	Result *bool   `json:"result,omitempty"`
	Error  *string `json:"error,omitempty"`
}

func (s *BifrostHTTPServer) handleValidateGuardrailRule(ctx *fasthttp.RequestCtx) {
	var req validateRuleRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		handlers.SendError(ctx, http.StatusBadRequest, err.Error())
		return
	}

	env, err := guardrailsplugin.NewCELEnvPublic()
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	prog, err := guardrailsplugin.CompileExpressionPublic(env, req.CelExpression)
	if err != nil {
		errStr := err.Error()
		handlers.SendJSONWithStatus(ctx, validateRuleResponse{Valid: false, Error: &errStr}, http.StatusOK)
		return
	}

	vars := map[string]interface{}{
		"request": req.Sample,
		"output":  map[string]interface{}{},
	}
	result, err := guardrailsplugin.EvalProgramPublic(prog, vars)
	if err != nil {
		errStr := err.Error()
		handlers.SendJSONWithStatus(ctx, validateRuleResponse{Valid: true, Error: &errStr}, http.StatusOK)
		return
	}

	handlers.SendJSONWithStatus(ctx, validateRuleResponse{Valid: true, Result: &result}, http.StatusOK)
}

func (s *BifrostHTTPServer) getGuardrailsPlugin() (*guardrailsplugin.GuardrailsPlugin, error) {
	for _, p := range s.Config.GetLoadedLLMPlugins() {
		if p.GetName() != guardrailsplugin.PluginName {
			continue
		}
		gp, ok := p.(*guardrailsplugin.GuardrailsPlugin)
		if !ok {
			return nil, fmt.Errorf("guardrails plugin type mismatch")
		}
		return gp, nil
	}
	return nil, fmt.Errorf("guardrails plugin not loaded")
}

type guardrailProfileResponse struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	ProviderName string                 `json:"provider_name"`
	Enabled      bool                   `json:"enabled"`
	Config       map[string]interface{} `json:"config"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

func toGuardrailProfileResponse(profile *tables.TableGuardrailProfile) (guardrailProfileResponse, error) {
	resp := guardrailProfileResponse{
		ID:           profile.ID,
		Name:         profile.Name,
		ProviderName: profile.ProviderName,
		Enabled:      profile.Enabled,
		CreatedAt:    profile.CreatedAt,
		UpdatedAt:    profile.UpdatedAt,
		Config:       map[string]interface{}{},
	}
	if profile.ConfigJSON == "" {
		return resp, nil
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(profile.ConfigJSON), &cfg); err != nil {
		return guardrailProfileResponse{}, fmt.Errorf("parse guardrail profile config: %w", err)
	}
	resp.Config = cfg
	if resp.Config == nil {
		resp.Config = map[string]interface{}{}
	}
	return resp, nil
}
