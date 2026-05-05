// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package eval

import (
	"context"
	"fmt"
	"sync"

	"github.com/fallguy/rimsky/modeling/qualityrule"
)

var (
	evalsMu sync.RWMutex
	evals   = map[string]qualityrule.Evaluator{}
)

// Register associates an Evaluator with a rule type name. Safe for concurrent
// use. Consumers register "custom" handlers under their own unique names.
func Register(name string, ev qualityrule.Evaluator) {
	evalsMu.Lock()
	defer evalsMu.Unlock()
	evals[name] = ev
}

// Get looks up a registered Evaluator by name.
func Get(name string) (qualityrule.Evaluator, bool) {
	evalsMu.RLock()
	defer evalsMu.RUnlock()
	e, ok := evals[name]
	return e, ok
}

// EvaluateAll runs a set of Specs over the input, partitioning Failures by
// severity. err-severity failures should block a commit; warning-severity
// failures should be logged but not block.
func EvaluateAll(ctx context.Context, specs []qualityrule.Spec, input qualityrule.EvalInput) ([]qualityrule.Failure, []qualityrule.Failure, error) {
	var errors, warnings []qualityrule.Failure
	for _, s := range specs {
		ev, ok := Get(s.Type)
		if !ok {
			if s.Type == "custom" {
				// Custom rules are expected to be registered by consumers.
				// Fail evaluation rather than silently skip.
				return nil, nil, fmt.Errorf("qualityrule: no custom handler registered: %+v", s.Config)
			}
			return nil, nil, fmt.Errorf("qualityrule: unknown rule type %q", s.Type)
		}
		in := qualityrule.EvalInput{NewData: input.NewData, PreviousData: input.PreviousData, Cfg: s.Config}
		passed, details, err := ev.Evaluate(ctx, in)
		if err != nil {
			return nil, nil, fmt.Errorf("qualityrule %q: %w", s.Type, err)
		}
		if !passed {
			f := qualityrule.Failure{RuleType: s.Type, Config: s.Config, Severity: s.Severity, Details: details}
			if s.Severity == "warning" {
				warnings = append(warnings, f)
			} else {
				// default severity is error
				errors = append(errors, f)
			}
		}
	}
	return errors, warnings, nil
}

// --- builtins ---

// rowCountRatioEvaluator: len(new) >= cfg["min_ratio"] * len(previous). Skipped
// if no previous.
type rowCountRatioEvaluator struct{}

func (rowCountRatioEvaluator) Evaluate(_ context.Context, in qualityrule.EvalInput) (bool, string, error) {
	minRatio, _ := toFloat(in.Cfg["min_ratio"])
	newLen, _ := lenOf(in.NewData)
	if in.PreviousData == nil {
		return true, "", nil
	}
	prevLen, _ := lenOf(in.PreviousData)
	if prevLen == 0 {
		return true, "", nil
	}
	ratio := float64(newLen) / float64(prevLen)
	if ratio < minRatio {
		return false, fmt.Sprintf("row count %d is less than %.2f * previous %d", newLen, minRatio, prevLen), nil
	}
	return true, "", nil
}

// noNullsEvaluator: cfg["fields"] = []string; every record must have non-null
// values for each listed field.
type noNullsEvaluator struct{}

func (noNullsEvaluator) Evaluate(_ context.Context, in qualityrule.EvalInput) (bool, string, error) {
	fields, _ := in.Cfg["fields"].([]any)
	rows, ok := in.NewData.([]map[string]any)
	if !ok {
		if rowsMaybe, ok2 := coerceToRecordList(in.NewData); ok2 {
			rows = rowsMaybe
		} else {
			return false, "input not a list of records", nil
		}
	}
	for i, row := range rows {
		for _, f := range fields {
			key, ok := f.(string)
			if !ok {
				continue
			}
			v, present := row[key]
			if !present || v == nil {
				return false, fmt.Sprintf("row %d field %q is null", i, key), nil
			}
		}
	}
	return true, "", nil
}

// nullableFieldsPresentEvaluator: cfg["fields"] = []string; each field must
// exist (may be null) in every record.
type nullableFieldsPresentEvaluator struct{}

func (nullableFieldsPresentEvaluator) Evaluate(_ context.Context, in qualityrule.EvalInput) (bool, string, error) {
	fields, _ := in.Cfg["fields"].([]any)
	rows, ok := in.NewData.([]map[string]any)
	if !ok {
		if rowsMaybe, ok2 := coerceToRecordList(in.NewData); ok2 {
			rows = rowsMaybe
		} else {
			return false, "input not a list of records", nil
		}
	}
	for i, row := range rows {
		for _, f := range fields {
			key, ok := f.(string)
			if !ok {
				continue
			}
			if _, present := row[key]; !present {
				return false, fmt.Sprintf("row %d missing field %q", i, key), nil
			}
		}
	}
	return true, "", nil
}

func init() {
	Register("row_count_ratio", rowCountRatioEvaluator{})
	Register("no_nulls", noNullsEvaluator{})
	Register("nullable_fields_present", nullableFieldsPresentEvaluator{})
	// "custom" is not pre-registered — consumers register their own handlers
	// by name.
}

// --- helpers ---

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func lenOf(v any) (int, bool) {
	switch x := v.(type) {
	case []any:
		return len(x), true
	case []map[string]any:
		return len(x), true
	}
	return 0, false
}

func coerceToRecordList(v any) ([]map[string]any, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, m)
	}
	return out, true
}
