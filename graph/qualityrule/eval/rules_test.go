// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package eval

import (
	"context"
	"testing"

	"github.com/fallguy/rimsky/graph/qualityrule"
	"github.com/fallguy/rimsky/graph/shared"
	"github.com/stretchr/testify/require"
)

func TestRowCountRatio_PassesOnRatioMet(t *testing.T) {
	ev, ok := Get("row_count_ratio")
	require.True(t, ok)

	newRows := []map[string]any{{"a": 1}, {"a": 2}, {"a": 3}, {"a": 4}, {"a": 5}}
	prevRows := []map[string]any{{"a": 1}, {"a": 2}, {"a": 3}, {"a": 4}, {"a": 5}, {"a": 6}}

	passed, details, err := ev.Evaluate(context.Background(), qualityrule.EvalInput{
		NewData:      newRows,
		PreviousData: prevRows,
		Cfg:          map[string]any{"min_ratio": 0.8},
	})
	require.NoError(t, err)
	require.True(t, passed, "expected pass, got details=%q", details)
}

func TestRowCountRatio_FailsOnRatioUnmet(t *testing.T) {
	ev, ok := Get("row_count_ratio")
	require.True(t, ok)

	newRows := []map[string]any{{"a": 1}, {"a": 2}}
	prevRows := []map[string]any{{"a": 1}, {"a": 2}, {"a": 3}, {"a": 4}, {"a": 5}}

	passed, details, err := ev.Evaluate(context.Background(), qualityrule.EvalInput{
		NewData:      newRows,
		PreviousData: prevRows,
		Cfg:          map[string]any{"min_ratio": 0.9},
	})
	require.NoError(t, err)
	require.False(t, passed)
	require.Contains(t, details, "row count 2")
}

func TestRowCountRatio_SkippedWhenNoPrevious(t *testing.T) {
	ev, ok := Get("row_count_ratio")
	require.True(t, ok)

	passed, _, err := ev.Evaluate(context.Background(), qualityrule.EvalInput{
		NewData:      []map[string]any{{"a": 1}},
		PreviousData: nil,
		Cfg:          map[string]any{"min_ratio": 0.9},
	})
	require.NoError(t, err)
	require.True(t, passed)
}

func TestNoNulls_PassesWhenAllPresent(t *testing.T) {
	ev, ok := Get("no_nulls")
	require.True(t, ok)

	rows := []map[string]any{{"id": 1, "name": "a"}, {"id": 2, "name": "b"}}

	passed, details, err := ev.Evaluate(context.Background(), qualityrule.EvalInput{
		NewData: rows,
		Cfg:     map[string]any{"fields": []any{"id", "name"}},
	})
	require.NoError(t, err)
	require.True(t, passed, "expected pass, got details=%q", details)
}

func TestNoNulls_FailsOnNullInRow(t *testing.T) {
	ev, ok := Get("no_nulls")
	require.True(t, ok)

	rows := []map[string]any{{"id": 1, "name": "a"}, {"id": 2, "name": nil}}

	passed, details, err := ev.Evaluate(context.Background(), qualityrule.EvalInput{
		NewData: rows,
		Cfg:     map[string]any{"fields": []any{"id", "name"}},
	})
	require.NoError(t, err)
	require.False(t, passed)
	require.Contains(t, details, "row 1")
	require.Contains(t, details, "name")
}

func TestNullableFieldsPresent_FailsOnMissingField(t *testing.T) {
	ev, ok := Get("nullable_fields_present")
	require.True(t, ok)

	rows := []map[string]any{{"id": 1, "name": "a"}, {"id": 2}} // row 1 missing "name"

	passed, details, err := ev.Evaluate(context.Background(), qualityrule.EvalInput{
		NewData: rows,
		Cfg:     map[string]any{"fields": []any{"id", "name"}},
	})
	require.NoError(t, err)
	require.False(t, passed)
	require.Contains(t, details, "row 1")
	require.Contains(t, details, "name")
}

func TestCustomRuleNotRegistered_ReturnsError(t *testing.T) {
	specs := []qualityrule.Spec{{Type: "custom", Config: map[string]any{"handler": "my_handler"}, Severity: shared.SeverityError}}

	_, _, err := EvaluateAll(context.Background(), specs, qualityrule.EvalInput{NewData: []map[string]any{}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "custom")
}

func TestUnknownRuleType_ReturnsError(t *testing.T) {
	specs := []qualityrule.Spec{{Type: "bogus_never_registered", Severity: shared.SeverityError}}

	_, _, err := EvaluateAll(context.Background(), specs, qualityrule.EvalInput{NewData: []map[string]any{}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown rule type")
}

func TestEvaluateAll_PartitionsBySeverity(t *testing.T) {
	// One error-severity failure, one warning-severity failure, one pass.
	rows := []map[string]any{{"id": 1}} // missing "name"

	specs := []qualityrule.Spec{
		// Pass: id present.
		{Type: "no_nulls", Config: map[string]any{"fields": []any{"id"}}, Severity: shared.SeverityError},
		// Fail (warning): name missing.
		{Type: "nullable_fields_present", Config: map[string]any{"fields": []any{"name"}}, Severity: shared.SeverityWarning},
		// Fail (error, via default empty severity): name is null.
		{Type: "no_nulls", Config: map[string]any{"fields": []any{"name"}}},
	}

	errs, warns, err := EvaluateAll(context.Background(), specs, qualityrule.EvalInput{NewData: rows})
	require.NoError(t, err)
	require.Len(t, errs, 1)
	require.Len(t, warns, 1)
	require.Equal(t, "no_nulls", errs[0].RuleType)
	require.Equal(t, "nullable_fields_present", warns[0].RuleType)
	require.Equal(t, shared.SeverityWarning, warns[0].Severity)
}
