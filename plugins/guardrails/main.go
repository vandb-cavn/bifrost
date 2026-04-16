package guardrails

import (
	"context"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// PluginName is the built-in guardrails plugin name.
const PluginName = "guardrails"

const (
	guardrailWarnedKey         schemas.BifrostContextKey = "bf-guardrail-warned"
	guardrailInputProfilesKey  schemas.BifrostContextKey = "bf-guardrail-input-profiles"
	guardrailOutputProfilesKey schemas.BifrostContextKey = "bf-guardrail-output-profiles"
	// guardrailRequestMessagesKey stores extracted chat messages from the original request so
	// PostLLMHook output CEL rules can use request.messages (e.g. hallucination vs input).
	guardrailRequestMessagesKey schemas.BifrostContextKey = "bf-guardrail-req-messages"
	// guardrailRequestModelKey stores the model from the inbound request for output CEL request.model.
	guardrailRequestModelKey schemas.BifrostContextKey = "bf-guardrail-req-model"
)

// GuardrailsPlugin enforces content-safety rules via CEL and optional external providers.
type GuardrailsPlugin struct {
	cache       *rulesCache
	clients     map[string]ProfileClient
	celEnv      *cel.Env
	configStore configstore.ConfigStore
	logger      schemas.Logger
}

// Init loads rules and profiles from the config store and builds profile clients.
func Init(ctx context.Context, cs configstore.ConfigStore, logger schemas.Logger) (*GuardrailsPlugin, error) {
	env, err := newCELEnv()
	if err != nil {
		return nil, fmt.Errorf("guardrails: CEL env init failed: %w", err)
	}

	p := &GuardrailsPlugin{
		cache:       newRulesCache(env),
		clients:     make(map[string]ProfileClient),
		celEnv:      env,
		configStore: cs,
		logger:      logger,
	}

	profiles, err := cs.GetGuardrailProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("guardrails: load profiles: %w", err)
	}
	p.ReloadProfiles(profiles)

	rules, err := cs.GetGuardrailRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("guardrails: load rules: %w", err)
	}
	p.ReloadRules(rules)

	return p, nil
}

func (p *GuardrailsPlugin) GetName() string { return PluginName }

func (p *GuardrailsPlugin) Cleanup() error { return nil }

// UpsertRule updates a single rule in the cache (config sync).
func (p *GuardrailsPlugin) UpsertRule(rule *configstoreTables.TableGuardrailRule) {
	if err := p.cache.upsertRule(rule); err != nil {
		p.logger.Warn("guardrails: rule %q CEL compile failed, rule disabled: %v", rule.ID, err)
	}
}

func (p *GuardrailsPlugin) DeleteRule(id string) {
	p.cache.deleteRule(id)
}

// UpsertProfile updates a profile and its HTTP client.
func (p *GuardrailsPlugin) UpsertProfile(profile *configstoreTables.TableGuardrailProfile) {
	p.cache.upsertProfile(profile)
	if profile.Enabled {
		client, err := newProfileClient(profile)
		if err != nil {
			p.logger.Warn("guardrails: profile %q client build failed: %v", profile.ID, err)
			return
		}
		p.clients[profile.ID] = client
	} else {
		delete(p.clients, profile.ID)
	}
}

func (p *GuardrailsPlugin) DeleteProfile(id string) {
	p.cache.deleteProfile(id)
	delete(p.clients, id)
}

// ReloadRules replaces all rules (FullReload).
func (p *GuardrailsPlugin) ReloadRules(rules []*configstoreTables.TableGuardrailRule) {
	p.cache.reloadRules(rules)
}

// ReloadProfiles replaces all profiles and rebuilds clients (FullReload).
func (p *GuardrailsPlugin) ReloadProfiles(profiles []*configstoreTables.TableGuardrailProfile) {
	p.cache.reloadProfiles(profiles)
	newClients := make(map[string]ProfileClient, len(profiles))
	for _, prof := range profiles {
		if !prof.Enabled {
			continue
		}
		client, err := newProfileClient(prof)
		if err != nil {
			p.logger.Warn("guardrails: profile %q client build failed: %v", prof.ID, err)
			continue
		}
		newClients[prof.ID] = client
	}
	p.clients = newClients
}

func getVKAndTeamFromContext(ctx *schemas.BifrostContext) (vkID, teamID string) {
	if v := ctx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID); v != nil {
		vkID, _ = v.(string)
	}
	if v := ctx.Value(schemas.BifrostContextKeyGovernanceTeamID); v != nil {
		teamID, _ = v.(string)
	}
	return
}
