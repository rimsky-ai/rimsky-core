package qualityrule

import (
	"context"

	"github.com/fallguy/rimsky/core/shared"
)

// Spec is a single quality rule declaration: a rule type (builtin name or
// "custom"), a free-form config payload consumed by the evaluator, and a
// severity classifying whether a failure should block a commit or merely warn.
type Spec struct {
	Type     string
	Config   map[string]any
	Severity shared.Severity // default "error"
}

// Failure is the outcome of a single Spec that did not pass, carrying enough
// context for the caller to log or surface the problem.
type Failure struct {
	RuleType string
	Config   map[string]any
	Severity shared.Severity
	Details  string
}

// EvalInput is the payload handed to an Evaluator. NewData is the proposed
// data; PreviousData is nil when there is no prior version. Cfg is the
// Spec.Config for the rule being evaluated.
type EvalInput struct {
	NewData      any
	PreviousData any
	Cfg          map[string]any
}

// Evaluator runs a single quality rule. It returns whether the rule passed,
// optional failure details, and any execution error. Implementations should be
// pure functions of their input and free of side effects.
type Evaluator interface {
	Evaluate(ctx context.Context, input EvalInput) (passed bool, details string, err error)
}
