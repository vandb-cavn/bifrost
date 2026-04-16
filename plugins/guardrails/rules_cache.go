package guardrails

import (
	"fmt"
	"sort"
	"sync"

	"github.com/google/cel-go/cel"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

type cachedRule struct {
	rule    *configstoreTables.TableGuardrailRule
	program cel.Program
}

type rulesCache struct {
	mu       sync.RWMutex
	rules    map[string]*cachedRule
	profiles map[string]*configstoreTables.TableGuardrailProfile
	celEnv   *cel.Env
}

func newRulesCache(env *cel.Env) *rulesCache {
	return &rulesCache{
		rules:    make(map[string]*cachedRule),
		profiles: make(map[string]*configstoreTables.TableGuardrailProfile),
		celEnv:   env,
	}
}

func (c *rulesCache) upsertRule(rule *configstoreTables.TableGuardrailRule) error {
	if !rule.Enabled {
		c.mu.Lock()
		delete(c.rules, rule.ID)
		c.mu.Unlock()
		return nil
	}
	prog, err := compileExpression(c.celEnv, rule.CelExpression)
	if err != nil {
		return fmt.Errorf("rule %q CEL compile failed (rule disabled): %w", rule.ID, err)
	}
	c.mu.Lock()
	c.rules[rule.ID] = &cachedRule{rule: rule, program: prog}
	c.mu.Unlock()
	return nil
}

func (c *rulesCache) deleteRule(id string) {
	c.mu.Lock()
	delete(c.rules, id)
	c.mu.Unlock()
}

func (c *rulesCache) upsertProfile(profile *configstoreTables.TableGuardrailProfile) {
	c.mu.Lock()
	if profile.Enabled {
		c.profiles[profile.ID] = profile
	} else {
		delete(c.profiles, profile.ID)
	}
	c.mu.Unlock()
}

func (c *rulesCache) deleteProfile(id string) {
	c.mu.Lock()
	delete(c.profiles, id)
	c.mu.Unlock()
}

func (c *rulesCache) getProfile(id string) *configstoreTables.TableGuardrailProfile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profiles[id]
}

func (c *rulesCache) getInputRules(vkID, teamID string) []*cachedRule {
	return c.getRulesByApplyTo([]string{"input", "both"}, vkID, teamID)
}

func (c *rulesCache) getOutputRules(vkID, teamID string) []*cachedRule {
	return c.getRulesByApplyTo([]string{"output", "both"}, vkID, teamID)
}

func (c *rulesCache) getRulesByApplyTo(applyTo []string, vkID, teamID string) []*cachedRule {
	c.mu.RLock()
	defer c.mu.RUnlock()

	applySet := make(map[string]struct{}, len(applyTo))
	for _, a := range applyTo {
		applySet[a] = struct{}{}
	}

	var result []*cachedRule
	for _, cr := range c.rules {
		if _, ok := applySet[cr.rule.ApplyTo]; !ok {
			continue
		}
		if !matchesScope(cr.rule, vkID, teamID) {
			continue
		}
		result = append(result, cr)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].rule.Priority < result[j].rule.Priority
	})
	return result
}

func matchesScope(rule *configstoreTables.TableGuardrailRule, vkID, teamID string) bool {
	switch rule.Scope {
	case "global":
		return true
	case "virtual_key":
		return rule.ScopeID != nil && *rule.ScopeID == vkID
	case "team":
		return rule.ScopeID != nil && *rule.ScopeID == teamID
	default:
		return false
	}
}

func (c *rulesCache) reloadRules(rules []*configstoreTables.TableGuardrailRule) {
	newMap := make(map[string]*cachedRule, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		prog, err := compileExpression(c.celEnv, rule.CelExpression)
		if err != nil {
			continue
		}
		newMap[rule.ID] = &cachedRule{rule: rule, program: prog}
	}
	c.mu.Lock()
	c.rules = newMap
	c.mu.Unlock()
}

func (c *rulesCache) reloadProfiles(profiles []*configstoreTables.TableGuardrailProfile) {
	newMap := make(map[string]*configstoreTables.TableGuardrailProfile, len(profiles))
	for _, p := range profiles {
		if p.Enabled {
			newMap[p.ID] = p
		}
	}
	c.mu.Lock()
	c.profiles = newMap
	c.mu.Unlock()
}
