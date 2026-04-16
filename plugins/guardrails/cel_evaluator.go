package guardrails

import (
	"fmt"

	"github.com/google/cel-go/cel"
)

// newCELEnv creates the singleton CEL environment for guardrail expression evaluation.
func newCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("output", cel.MapType(cel.StringType, cel.DynType)),
	)
}

// compileExpression compiles a CEL expression string into a cel.Program.
func compileExpression(env *cel.Env, expr string) (cel.Program, error) {
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("CEL compile error: %w", issues.Err())
	}
	prog, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("CEL program error: %w", err)
	}
	return prog, nil
}

// evalProgram evaluates a pre-compiled CEL program with the given variable bindings.
func evalProgram(prog cel.Program, vars map[string]interface{}) (bool, error) {
	merged := make(map[string]interface{}, len(vars)+2)
	for k, v := range vars {
		merged[k] = v
	}
	if _, ok := merged["output"]; !ok {
		merged["output"] = map[string]interface{}{}
	}
	out, _, err := prog.Eval(merged)
	if err != nil {
		return false, fmt.Errorf("CEL eval error: %w", err)
	}
	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression must return bool, got %T", out.Value())
	}
	return result, nil
}

// NewCELEnvPublic is a public wrapper for HTTP validate endpoint.
func NewCELEnvPublic() (*cel.Env, error) { return newCELEnv() }

// CompileExpressionPublic compiles a CEL expression (validate endpoint).
func CompileExpressionPublic(env *cel.Env, expr string) (cel.Program, error) {
	return compileExpression(env, expr)
}

// EvalProgramPublic evaluates a program (validate endpoint).
func EvalProgramPublic(prog cel.Program, vars map[string]interface{}) (bool, error) {
	return evalProgram(prog, vars)
}
