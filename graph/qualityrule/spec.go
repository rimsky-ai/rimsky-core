// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package qualityrule

import (
	"context"

	"github.com/fallguy/rimsky/foundation/spec"
)

// Spec / Failure / EvalInput are aliased from foundation/spec because
// they appear on persisted rows: TemplateSpec.Nodes[].QualityRules is a
// []spec.QualityRuleSpec. The Evaluator interface (below) stays here in
// graph/qualityrule — it is the algorithm contract, not data.

type Spec = spec.QualityRuleSpec

type Failure = spec.QualityRuleFailure

type EvalInput = spec.QualityRuleEvalInput

// Evaluator runs a single quality rule. It returns whether the rule passed,
// optional failure details, and any execution error. Implementations should be
// pure functions of their input and free of side effects.
type Evaluator interface {
	Evaluate(ctx context.Context, input EvalInput) (passed bool, details string, err error)
}
