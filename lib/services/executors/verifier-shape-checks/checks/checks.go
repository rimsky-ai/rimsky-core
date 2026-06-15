// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package checks implements the shape-check primitives the
// verifier-shape-checks executor runs against a tabular payload. Each
// check has a `Kind` discriminator, a `Run` method, and a small per-
// kind config struct. Checks operate on an in-memory `[]Row` (one row
// = `map[string]any`); the caller is responsible for loading the data
// from the upstream claim's address.
//
// @deliberate: implements the verifier-executor pattern
// (documentation-only, no successor concept).
package checks

import (
	"fmt"
	"reflect"
	"regexp"
)

// Row is one logical record. The verifier protocol does not pin a row
// type; we use a JSON-shaped `map[string]any` because that matches both
// JSON wire payloads and Parquet/Arrow column-major loaders that
// convert per-row dict-of-fields output.
type Row = map[string]any

// Result is the per-check outcome. The aggregator at the executor
// boundary turns N results into a single StreamClose terminal: any
// `Pass=false` → `Error{error_class: "verifier/check_failed/<kind>"}`
// (carrying the first failing check's `kind` suffix per
// `concept:signal`); otherwise `Success{changed: false}` (verifiers do
// not mutate state).
type Result struct {
	Kind    string
	Pass    bool
	Failed  []Row    // @constraint: populated only when Pass=false; capped at 100 rows by appendBounded.
	Counts  Counters // @constraint: diagnostic counters returned to the executor for aggregation.
	Message string   // @constraint: human-readable summary surfaced in the Error terminal.
}

// Counters carry numeric diagnostics shared across the check set.
type Counters struct {
	Rows      int
	Failed    int
	Threshold float64 // @constraint: populated by ratio-comparing checks (row_count_ratio); zero otherwise.
}

// Severity classifies how a failed check is treated at run time. An
// `error`-severity failure blocks the commit (drives the Error terminal);
// a `warning`-severity failure is non-blocking and surfaced as a soft
// finding while the dispatch still succeeds.
//
// This is a services-local copy of the platform's spec.Severity enum.
// It is duplicated rather than imported because the consumption-side
// services module (lib/services) is forbidden from importing lib/foundation
// (the `consumption-side-isolation` depguard rule); the type is small,
// stable, and the wire-string values match spec.Severity exactly.
//
//	@source: lib/foundation/spec/enums.go::Severity
type Severity string

const (
	// SeverityError marks a check whose failure blocks the commit.
	SeverityError Severity = "error"
	// SeverityWarning marks a check whose failure is non-blocking.
	SeverityWarning Severity = "warning"
)

// CheckSpec is the discriminated-union shape callers serialize from
// the verifier executor's `attributes.checks`. The verifier deserializes
// `attributes.checks[*]` into this shape and dispatches to the per-kind
// runner. Severity defaults to SeverityError when unset by the caller —
// a failing check blocks unless the author explicitly downgrades it to
// a warning.
type CheckSpec struct {
	Kind     string         `json:"kind"`
	Config   map[string]any `json:"config"`
	Severity Severity       `json:"severity"`
}

// KnownKinds is the single source of truth for the check kinds Run
// dispatches on. The registration-time Validation mix-in consumes it
// so its `unknown_check_kind` advisory cannot drift from the runtime
// dispatcher — keep it in lockstep with Run's switch arms.
func KnownKinds() map[string]bool {
	return map[string]bool{
		"no_nulls":                true,
		"nullable_fields_present": true,
		"pk_unique":               true,
		"row_count_ratio":         true,
		"row_count_absolute":      true,
		"value_in_set":            true,
		"regex_match":             true,
		"numeric_range":           true,
	}
}

// Run dispatches a single check against rows. Returns a Result; the
// `Pass` flag drives terminal aggregation upstream. Unknown kinds
// produce a `Pass=false` Result with `Kind="unknown"` so the executor
// surfaces a recognizable error without panicking.
func Run(spec CheckSpec, rows []Row) Result {
	switch spec.Kind {
	case "no_nulls":
		return runNoNulls(spec.Config, rows)
	case "nullable_fields_present":
		return runNullableFieldsPresent(spec.Config, rows)
	case "pk_unique":
		return runPKUnique(spec.Config, rows)
	case "row_count_ratio":
		return runRowCountRatio(spec.Config, rows)
	case "row_count_absolute":
		return runRowCountAbsolute(spec.Config, rows)
	case "value_in_set":
		return runValueInSet(spec.Config, rows)
	case "regex_match":
		return runRegexMatch(spec.Config, rows)
	case "numeric_range":
		return runNumericRange(spec.Config, rows)
	}
	return Result{
		Kind:    "unknown",
		Pass:    false,
		Message: fmt.Sprintf("unknown check kind %q", spec.Kind),
	}
}

// fieldList parses a `field` (string) or `fields` ([]string) config
// entry into a normalized slice. Either key is accepted to keep
// attributes YAML ergonomic.
func fieldList(cfg map[string]any) ([]string, error) {
	if v, ok := cfg["field"].(string); ok && v != "" {
		return []string{v}, nil
	}
	if vs, ok := cfg["fields"].([]any); ok {
		out := make([]string, 0, len(vs))
		for _, v := range vs {
			s, ok := v.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("fields entry must be non-empty strings")
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("config: `field` (string) or `fields` ([]string) required")
}

// runNoNulls verifies every named field is non-null on every row.
func runNoNulls(cfg map[string]any, rows []Row) Result {
	fields, err := fieldList(cfg)
	if err != nil {
		return Result{Kind: "no_nulls", Message: err.Error()}
	}
	res := Result{Kind: "no_nulls", Counts: Counters{Rows: len(rows)}}
	for _, r := range rows {
		for _, f := range fields {
			v, present := r[f]
			if !present || v == nil {
				res.Failed = appendBounded(res.Failed, r, 100)
				res.Counts.Failed++
				break
			}
		}
	}
	res.Pass = res.Counts.Failed == 0
	if !res.Pass {
		res.Message = fmt.Sprintf("no_nulls: %d/%d rows have null values across fields %v",
			res.Counts.Failed, res.Counts.Rows, fields)
	}
	return res
}

// runNullableFieldsPresent verifies the named fields are present (key
// in the row map) regardless of value. Distinct from no_nulls: a
// present field with a nil value counts as present here.
func runNullableFieldsPresent(cfg map[string]any, rows []Row) Result {
	fields, err := fieldList(cfg)
	if err != nil {
		return Result{Kind: "nullable_fields_present", Message: err.Error()}
	}
	res := Result{Kind: "nullable_fields_present", Counts: Counters{Rows: len(rows)}}
	for _, r := range rows {
		for _, f := range fields {
			if _, present := r[f]; !present {
				res.Failed = appendBounded(res.Failed, r, 100)
				res.Counts.Failed++
				break
			}
		}
	}
	res.Pass = res.Counts.Failed == 0
	if !res.Pass {
		res.Message = fmt.Sprintf("nullable_fields_present: %d/%d rows missing one of %v",
			res.Counts.Failed, res.Counts.Rows, fields)
	}
	return res
}

// runPKUnique verifies the tuple `(field_1, ..., field_n)` is unique
// across rows. Duplicate tuples surface in Result.Failed.
func runPKUnique(cfg map[string]any, rows []Row) Result {
	fields, err := fieldList(cfg)
	if err != nil {
		return Result{Kind: "pk_unique", Message: err.Error()}
	}
	seen := map[string]Row{}
	res := Result{Kind: "pk_unique", Counts: Counters{Rows: len(rows)}}
	for _, r := range rows {
		key := pkKey(r, fields)
		if prev, ok := seen[key]; ok {
			res.Failed = appendBounded(res.Failed, r, 100)
			_ = prev
			res.Counts.Failed++
			continue
		}
		seen[key] = r
	}
	res.Pass = res.Counts.Failed == 0
	if !res.Pass {
		res.Message = fmt.Sprintf("pk_unique: %d duplicate tuples over fields %v",
			res.Counts.Failed, fields)
	}
	return res
}

func pkKey(r Row, fields []string) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = fmt.Sprintf("%v", r[f])
	}
	return joinParts(parts)
}

func joinParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\x1f" // @deliberate: ASCII unit separator avoids collision with field-value contents in the composite key.
		}
		out += p
	}
	return out
}

// runRowCountRatio verifies `len(rows) / baseline ∈ [low, high]`.
// `baseline` is supplied via config (config.baseline) — the executor
// reads it from the prior version's attribute (caller-pre-loaded) or
// from a static value.
func runRowCountRatio(cfg map[string]any, rows []Row) Result {
	baselineRaw, ok := cfg["baseline"]
	if !ok {
		return Result{Kind: "row_count_ratio", Message: "config.baseline required"}
	}
	baseline, ok := numeric(baselineRaw)
	if !ok || baseline <= 0 {
		return Result{Kind: "row_count_ratio", Message: "config.baseline must be a positive number"}
	}
	low, _ := numericDefault(cfg["low"], 0.5)
	high, _ := numericDefault(cfg["high"], 2.0)
	count := float64(len(rows))
	ratio := count / baseline
	res := Result{
		Kind:   "row_count_ratio",
		Counts: Counters{Rows: len(rows), Threshold: ratio},
	}
	if ratio < low || ratio > high {
		res.Message = fmt.Sprintf("row_count_ratio: ratio=%.2f outside [%g, %g] (rows=%d, baseline=%g)",
			ratio, low, high, len(rows), baseline)
		return res
	}
	res.Pass = true
	return res
}

// runRowCountAbsolute verifies `len(rows) ∈ [min, max]`. `max` is
// optional (omitting it means unbounded above).
func runRowCountAbsolute(cfg map[string]any, rows []Row) Result {
	minVal, ok := numeric(cfg["min"])
	if !ok {
		return Result{Kind: "row_count_absolute", Message: "config.min required"}
	}
	res := Result{Kind: "row_count_absolute", Counts: Counters{Rows: len(rows)}}
	if float64(len(rows)) < minVal {
		res.Message = fmt.Sprintf("row_count_absolute: %d rows < min %g", len(rows), minVal)
		return res
	}
	if maxRaw, ok := cfg["max"]; ok {
		if maxVal, ok := numeric(maxRaw); ok && float64(len(rows)) > maxVal {
			res.Message = fmt.Sprintf("row_count_absolute: %d rows > max %g", len(rows), maxVal)
			return res
		}
	}
	res.Pass = true
	return res
}

// runValueInSet verifies every row's `field` value is in `set`.
func runValueInSet(cfg map[string]any, rows []Row) Result {
	field, _ := cfg["field"].(string)
	if field == "" {
		return Result{Kind: "value_in_set", Message: "config.field required"}
	}
	set := map[string]struct{}{}
	if vs, ok := cfg["set"].([]any); ok {
		for _, v := range vs {
			set[fmt.Sprintf("%v", v)] = struct{}{}
		}
	}
	if len(set) == 0 {
		return Result{Kind: "value_in_set", Message: "config.set required"}
	}
	res := Result{Kind: "value_in_set", Counts: Counters{Rows: len(rows)}}
	for _, r := range rows {
		v := r[field]
		if _, ok := set[fmt.Sprintf("%v", v)]; !ok {
			res.Failed = appendBounded(res.Failed, r, 100)
			res.Counts.Failed++
		}
	}
	res.Pass = res.Counts.Failed == 0
	if !res.Pass {
		res.Message = fmt.Sprintf("value_in_set: %d/%d rows have value not in set for field %q",
			res.Counts.Failed, res.Counts.Rows, field)
	}
	return res
}

// runRegexMatch verifies every row's `field` value matches `pattern`.
func runRegexMatch(cfg map[string]any, rows []Row) Result {
	field, _ := cfg["field"].(string)
	if field == "" {
		return Result{Kind: "regex_match", Message: "config.field required"}
	}
	pattern, _ := cfg["pattern"].(string)
	if pattern == "" {
		return Result{Kind: "regex_match", Message: "config.pattern required"}
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{Kind: "regex_match", Message: "config.pattern compile: " + err.Error()}
	}
	res := Result{Kind: "regex_match", Counts: Counters{Rows: len(rows)}}
	for _, r := range rows {
		v, _ := r[field].(string)
		if !re.MatchString(v) {
			res.Failed = appendBounded(res.Failed, r, 100)
			res.Counts.Failed++
		}
	}
	res.Pass = res.Counts.Failed == 0
	if !res.Pass {
		res.Message = fmt.Sprintf("regex_match: %d/%d rows have field %q not matching %q",
			res.Counts.Failed, res.Counts.Rows, field, pattern)
	}
	return res
}

// runNumericRange verifies every row's `field` value falls in
// [min, max]. Either bound is optional; omitting both is an error.
func runNumericRange(cfg map[string]any, rows []Row) Result {
	field, _ := cfg["field"].(string)
	if field == "" {
		return Result{Kind: "numeric_range", Message: "config.field required"}
	}
	minRaw, hasMin := cfg["min"]
	maxRaw, hasMax := cfg["max"]
	if !hasMin && !hasMax {
		return Result{Kind: "numeric_range", Message: "config.min or config.max required"}
	}
	res := Result{Kind: "numeric_range", Counts: Counters{Rows: len(rows)}}
	for _, r := range rows {
		v, ok := numeric(r[field])
		if !ok {
			res.Failed = appendBounded(res.Failed, r, 100)
			res.Counts.Failed++
			continue
		}
		if hasMin {
			if min, ok := numeric(minRaw); ok && v < min {
				res.Failed = appendBounded(res.Failed, r, 100)
				res.Counts.Failed++
				continue
			}
		}
		if hasMax {
			if max, ok := numeric(maxRaw); ok && v > max {
				res.Failed = appendBounded(res.Failed, r, 100)
				res.Counts.Failed++
			}
		}
	}
	res.Pass = res.Counts.Failed == 0
	if !res.Pass {
		res.Message = fmt.Sprintf("numeric_range: %d/%d rows outside bounds for field %q",
			res.Counts.Failed, res.Counts.Rows, field)
	}
	return res
}

// numeric returns the float64 form of any numeric type emitted by
// encoding/json (float64), or zero+false when the value is not numeric.
func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	}
	// @deliberate: reflect-fallback handles numeric kinds non-JSON loaders may surface (e.g. Arrow Decimal128 as int64).
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	}
	return 0, false
}

// numericDefault parses v as numeric, falling back to def. The bool
// return reports whether the explicit value was used.
func numericDefault(v any, def float64) (float64, bool) {
	if x, ok := numeric(v); ok {
		return x, true
	}
	return def, false
}

// appendBounded keeps the failed-rows slice short to avoid OOMs on
// very-large failure sets. 100 rows is enough for operator
// diagnostics.
func appendBounded(out []Row, r Row, cap int) []Row {
	if len(out) >= cap {
		return out
	}
	return append(out, r)
}
