package governance

import (
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCELComplexityTierVariable proves that CEL supports the flat complexity_tier string variable.
// This is the foundation for expressions like complexity_tier == "COMPLEX".
func TestCELComplexityTierVariable(t *testing.T) {
	env, err := cel.NewEnv(
		cel.Variable("complexity_tier", cel.StringType),
	)
	require.NoError(t, err, "failed to create CEL environment")

	tests := []struct {
		name       string
		expression string
		variables  map[string]interface{}
		expected   bool
	}{
		{
			name:       "tier equals COMPLEX",
			expression: `complexity_tier == "COMPLEX"`,
			variables: map[string]interface{}{
				"complexity_tier": "COMPLEX",
			},
			expected: true,
		},
		{
			name:       "tier equals SIMPLE",
			expression: `complexity_tier == "SIMPLE"`,
			variables: map[string]interface{}{
				"complexity_tier": "SIMPLE",
			},
			expected: true,
		},
		{
			name:       "tier equals REASONING",
			expression: `complexity_tier == "REASONING"`,
			variables: map[string]interface{}{
				"complexity_tier": "REASONING",
			},
			expected: true,
		},
		{
			name:       "tier mismatch",
			expression: `complexity_tier == "COMPLEX"`,
			variables: map[string]interface{}{
				"complexity_tier": "MEDIUM",
			},
			expected: false,
		},
		{
			name:       "tier not equals",
			expression: `complexity_tier != "SIMPLE"`,
			variables: map[string]interface{}{
				"complexity_tier": "COMPLEX",
			},
			expected: true,
		},
		{
			name:       "unavailable complexity is empty string",
			expression: `complexity_tier == ""`,
			variables: map[string]interface{}{
				"complexity_tier": "",
			},
			expected: true,
		},
		{
			name:       "unavailable complexity does not match any tier",
			expression: `complexity_tier == "SIMPLE"`,
			variables: map[string]interface{}{
				"complexity_tier": "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast, issues := env.Compile(tt.expression)
			require.NoError(t, issues.Err(), "compilation failed for: %s", tt.expression)

			program, err := env.Program(ast)
			require.NoError(t, err, "program creation failed for: %s", tt.expression)

			out, _, err := program.Eval(tt.variables)
			require.NoError(t, err, "evaluation failed for: %s", tt.expression)

			result, ok := out.Value().(bool)
			assert.True(t, ok, "expected boolean result")
			assert.Equal(t, tt.expected, result, "unexpected result for: %s", tt.expression)
		})
	}
}

// TestCELComplexityWithFullEnvironment tests complexity_tier alongside all existing CEL variables.
func TestCELComplexityWithFullEnvironment(t *testing.T) {
	env, err := createCELEnvironment()
	require.NoError(t, err, "failed to create full CEL environment")

	expression := `complexity_tier == "SIMPLE" && budget_used > 60.0`
	ast, issues := env.Compile(expression)
	require.NoError(t, issues.Err(), "compilation failed")

	program, err := env.Program(ast)
	require.NoError(t, err, "program creation failed")

	variables := map[string]interface{}{
		"model":            "gpt-4o",
		"provider":         "openai",
		"request_type":     "chat_completion",
		"headers":          map[string]string{},
		"params":           map[string]string{},
		"virtual_key_id":   "",
		"virtual_key_name": "",
		"team_id":          "",
		"team_name":        "",
		"customer_id":      "",
		"customer_name":    "",
		"tokens_used":      0.0,
		"request":          0.0,
		"budget_used":      75.0,
		"complexity_tier":  "SIMPLE",
	}

	out, _, err := program.Eval(variables)
	require.NoError(t, err, "evaluation failed")

	result, ok := out.Value().(bool)
	assert.True(t, ok, "expected boolean result")
	assert.True(t, result, "expected complexity_tier == SIMPLE && budget_used > 60 to match")
}
