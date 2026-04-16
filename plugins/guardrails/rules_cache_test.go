package guardrails

import (
	"testing"
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeRule(id, scope, scopeID, applyTo, action string, enabled bool) *configstoreTables.TableGuardrailRule {
	return &configstoreTables.TableGuardrailRule{
		ID:            id,
		Name:          "rule-" + id,
		Enabled:       enabled,
		CelExpression: "true",
		ApplyTo:       applyTo,
		Action:        action,
		SamplingRate:  100,
		TimeoutMs:     5000,
		Priority:      0,
		Scope:         scope,
		ScopeID:       scopeIDPtr(scopeID),
		FailOpen:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

func scopeIDPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func newTestCache(t *testing.T) *rulesCache {
	t.Helper()
	env, err := newCELEnv()
	require.NoError(t, err)
	return newRulesCache(env)
}

func TestRulesCache_GlobalRulesReturnedForAll(t *testing.T) {
	c := newTestCache(t)
	rule := makeRule("r1", "global", "", "input", "block", true)
	require.NoError(t, c.upsertRule(rule))

	rules := c.getInputRules("", "")
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].rule.ID)
}

func TestRulesCache_VirtualKeyScopedRuleFiltered(t *testing.T) {
	c := newTestCache(t)
	require.NoError(t, c.upsertRule(makeRule("r1", "virtual_key", "vk-abc", "input", "block", true)))

	assert.Empty(t, c.getInputRules("vk-xyz", ""))
	rules := c.getInputRules("vk-abc", "")
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].rule.ID)
}

func TestRulesCache_TeamScopedRuleFiltered(t *testing.T) {
	c := newTestCache(t)
	require.NoError(t, c.upsertRule(makeRule("r1", "team", "team-1", "both", "warn", true)))

	assert.Empty(t, c.getOutputRules("", "team-2"))
	rules := c.getOutputRules("", "team-1")
	require.Len(t, rules, 1)
}

func TestRulesCache_DisabledRuleExcluded(t *testing.T) {
	c := newTestCache(t)
	require.NoError(t, c.upsertRule(makeRule("r1", "global", "", "input", "block", false)))
	assert.Empty(t, c.getInputRules("", ""))
}

func TestRulesCache_ApplyToFilter(t *testing.T) {
	c := newTestCache(t)
	require.NoError(t, c.upsertRule(makeRule("r-input", "global", "", "input", "block", true)))
	require.NoError(t, c.upsertRule(makeRule("r-output", "global", "", "output", "block", true)))
	require.NoError(t, c.upsertRule(makeRule("r-both", "global", "", "both", "block", true)))

	inputRules := c.getInputRules("", "")
	assert.Len(t, inputRules, 2)

	outputRules := c.getOutputRules("", "")
	assert.Len(t, outputRules, 2)
}

func TestRulesCache_DeleteRule(t *testing.T) {
	c := newTestCache(t)
	require.NoError(t, c.upsertRule(makeRule("r1", "global", "", "input", "block", true)))
	c.deleteRule("r1")
	assert.Empty(t, c.getInputRules("", ""))
}

func TestRulesCache_InvalidCELRuleSkipped(t *testing.T) {
	c := newTestCache(t)
	rule := makeRule("r1", "global", "", "input", "block", true)
	rule.CelExpression = "this is invalid CEL !!!"
	err := c.upsertRule(rule)
	require.Error(t, err)
	assert.Empty(t, c.getInputRules("", ""))
}

func TestRulesCache_UpsertUpdatesExisting(t *testing.T) {
	c := newTestCache(t)
	rule := makeRule("r1", "global", "", "input", "block", true)
	require.NoError(t, c.upsertRule(rule))
	rule.Action = "warn"
	require.NoError(t, c.upsertRule(rule))

	rules := c.getInputRules("", "")
	require.Len(t, rules, 1)
	assert.Equal(t, "warn", rules[0].rule.Action)
}
