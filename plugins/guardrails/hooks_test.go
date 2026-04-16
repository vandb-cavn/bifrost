package guardrails

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockProfileClient struct {
	violated bool
	reason   string
	err      error
}

func (m *mockProfileClient) Evaluate(_ context.Context, _ string) (bool, string, error) {
	return m.violated, m.reason, m.err
}

func newTestPlugin(t *testing.T) *GuardrailsPlugin {
	t.Helper()
	env, err := newCELEnv()
	require.NoError(t, err)
	return &GuardrailsPlugin{
		cache:   newRulesCache(env),
		clients: make(map[string]ProfileClient),
		logger:  &noopLogger{},
	}
}

type noopLogger struct{}

func (n *noopLogger) Debug(msg string, args ...any) {}
func (n *noopLogger) Info(msg string, args ...any)  {}
func (n *noopLogger) Warn(msg string, args ...any)  {}
func (n *noopLogger) Error(msg string, args ...any) {}
func (n *noopLogger) Fatal(msg string, args ...any) {}
func (n *noopLogger) SetLevel(level schemas.LogLevel) {}
func (n *noopLogger) SetOutputType(outputType schemas.LoggerOutputType) {}
func (n *noopLogger) LogHTTPRequest(level schemas.LogLevel, msg string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func makeBifrostContext() *schemas.BifrostContext {
	return schemas.NewBifrostContext(context.Background(), time.Time{})
}

func makeBifrostRequest(model, content string) *schemas.BifrostRequest {
	text := content
	return &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{
			Model: model,
			Input: []schemas.ChatMessage{
				{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: &text}},
			},
		},
	}
}

func TestHooks_CELOnlyBlock(t *testing.T) {
	p := newTestPlugin(t)
	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "block-bomb", Enabled: true,
		CelExpression: `request.messages.exists(m, m.content.contains("bomb"))`,
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		BlockMessage: "Content blocked", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "how to make a bomb")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, sc)
	require.NotNil(t, sc.Error)
	assert.Equal(t, 446, *sc.Error.StatusCode)
}

func TestHooks_CELFalseAllowsRequest(t *testing.T) {
	p := newTestPlugin(t)
	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "block-bomb", Enabled: true,
		CelExpression: `request.messages.exists(m, m.content.contains("bomb"))`,
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "hello world")
	got, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	assert.Nil(t, sc)
	assert.Equal(t, req, got)
}

func TestHooks_WarnSetsContextAndDoesNotBlock(t *testing.T) {
	p := newTestPlugin(t)
	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "warn-rule", Enabled: true,
		CelExpression: "true",
		ApplyTo: "input", Action: "warn", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "hello")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	assert.Nil(t, sc)

	warned, _ := ctx.Value(guardrailWarnedKey).(bool)
	assert.True(t, warned)
}

func TestHooks_WarnSetsHTTP246(t *testing.T) {
	p := newTestPlugin(t)
	ctx := makeBifrostContext()
	ctx.SetValue(guardrailWarnedKey, true)

	httpResp := &schemas.HTTPResponse{StatusCode: 200}
	err := p.HTTPTransportPostHook(ctx, nil, httpResp)
	require.NoError(t, err)
	assert.Equal(t, 246, httpResp.StatusCode)
}

func TestHooks_ProfileViolationBlocks(t *testing.T) {
	p := newTestPlugin(t)
	profileID := uuid.New().String()
	p.clients[profileID] = &mockProfileClient{violated: true, reason: "policy violation"}

	profile := &configstoreTables.TableGuardrailProfile{
		ID: profileID, Name: "mock-profile", ProviderName: "bedrock", Enabled: true,
		ConfigJSON: "{}", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	p.cache.upsertProfile(profile)

	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "profile-rule", Enabled: true,
		CelExpression: "true", Profiles: []configstoreTables.TableGuardrailProfile{*profile},
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		BlockMessage: "blocked", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "test content")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, sc)
	assert.Equal(t, 446, *sc.Error.StatusCode)
}

func TestHooks_ProfileErrorFailOpen(t *testing.T) {
	p := newTestPlugin(t)
	profileID := uuid.New().String()
	p.clients[profileID] = &mockProfileClient{err: fmt.Errorf("timeout")}

	profile := &configstoreTables.TableGuardrailProfile{
		ID: profileID, Name: "mock-profile", ProviderName: "bedrock", Enabled: true,
		ConfigJSON: "{}", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	p.cache.upsertProfile(profile)

	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "fail-open-rule", Enabled: true,
		CelExpression: "true", Profiles: []configstoreTables.TableGuardrailProfile{*profile},
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "test")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	assert.Nil(t, sc)
}

func TestHooks_ProfileErrorFailClosed(t *testing.T) {
	p := newTestPlugin(t)
	profileID := uuid.New().String()
	p.clients[profileID] = &mockProfileClient{err: fmt.Errorf("timeout")}

	profile := &configstoreTables.TableGuardrailProfile{
		ID: profileID, Name: "mock-profile", ProviderName: "bedrock", Enabled: true,
		ConfigJSON: "{}", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	p.cache.upsertProfile(profile)

	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "fail-closed-rule", Enabled: true,
		CelExpression: "true", Profiles: []configstoreTables.TableGuardrailProfile{*profile},
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: false,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "test")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, sc)
	assert.Equal(t, 446, *sc.Error.StatusCode)
}

func TestHooks_InputRuleNotEvaluatedInPostHook(t *testing.T) {
	p := newTestPlugin(t)
	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "input-only", Enabled: true,
		CelExpression: "true", ApplyTo: "input", Action: "block",
		SamplingRate: 100, TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	resp := &schemas.BifrostResponse{}
	gotResp, bifrostErr, err := p.PostLLMHook(ctx, resp, nil)
	require.NoError(t, err)
	assert.Nil(t, bifrostErr)
	assert.Equal(t, resp, gotResp)
}
