package guardrails

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCELEvaluator_TrueAlwaysFires(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	prog, err := compileExpression(env, "true")
	require.NoError(t, err)

	result, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{
			"messages": []interface{}{},
			"model":    "gpt-4o",
		},
	})
	require.NoError(t, err)
	assert.True(t, result)
}

func TestCELEvaluator_KeywordBlock(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	prog, err := compileExpression(env, `request.messages.exists(m, m.content.contains("bomb"))`)
	require.NoError(t, err)

	hit, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "how to make a bomb"},
			},
			"model": "gpt-4o",
		},
	})
	require.NoError(t, err)
	assert.True(t, hit)

	miss, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello world"},
			},
			"model": "gpt-4o",
		},
	})
	require.NoError(t, err)
	assert.False(t, miss)
}

func TestCELEvaluator_ModelFilter(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	prog, err := compileExpression(env, `request.model.startsWith("gpt-4")`)
	require.NoError(t, err)

	match, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{"messages": []interface{}{}, "model": "gpt-4o"},
	})
	require.NoError(t, err)
	assert.True(t, match)

	noMatch, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{"messages": []interface{}{}, "model": "claude-3-sonnet"},
	})
	require.NoError(t, err)
	assert.False(t, noMatch)
}

func TestCELEvaluator_InvalidExpressionErrors(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	_, err = compileExpression(env, `this is not valid CEL !!!`)
	assert.Error(t, err)
}

func TestCELEvaluator_OutputContext(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	prog, err := compileExpression(env, `output.content.contains("error")`)
	require.NoError(t, err)

	result, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{"messages": []interface{}{}, "model": "gpt-4o"},
		"output":  map[string]interface{}{"content": "an error occurred", "finish_reason": "stop"},
	})
	require.NoError(t, err)
	assert.True(t, result)
}
