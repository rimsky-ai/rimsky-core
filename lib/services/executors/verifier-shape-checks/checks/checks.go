// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package checks

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/checkspec"
)

type Row = map[string]any

type Result struct {
	Kind        string
	Pass        bool
	Failed      []Row
	Counts      Counters
	Message     string
	ConfigError bool
}

type Counters struct {
	Rows      int
	Failed    int
	Threshold float64
}

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type CheckSpec struct {
	Kind     string         `json:"kind"`
	Config   map[string]any `json:"config"`
	Severity Severity       `json:"severity"`
}

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

func RequiresRows(kind string) bool {
	switch kind {
	case "row_count_ratio", "row_count_absolute":
		return false
	default:
		return true
	}
}

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
		Kind:        "unknown",
		Pass:        false,
		Message:     fmt.Sprintf("unknown check kind %q", spec.Kind),
		ConfigError: true,
	}
}

func runNoNulls(cfg map[string]any, rows []Row) Result {
	fields, err := checkspec.FieldList(cfg)
	if err != nil {
		return Result{Kind: "no_nulls", Message: err.Error(), ConfigError: true}
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

func runNullableFieldsPresent(cfg map[string]any, rows []Row) Result {
	fields, err := checkspec.FieldList(cfg)
	if err != nil {
		return Result{Kind: "nullable_fields_present", Message: err.Error(), ConfigError: true}
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

func runPKUnique(cfg map[string]any, rows []Row) Result {
	fields, err := checkspec.FieldList(cfg)
	if err != nil {
		return Result{Kind: "pk_unique", Message: err.Error(), ConfigError: true}
	}
	seen := map[string]struct{}{}
	res := Result{Kind: "pk_unique", Counts: Counters{Rows: len(rows)}}
	for _, r := range rows {
		key, complete := pkKey(r, fields)
		if !complete {
			res.Failed = appendBounded(res.Failed, r, 100)
			res.Counts.Failed++
			continue
		}
		if _, dup := seen[key]; dup {
			res.Failed = appendBounded(res.Failed, r, 100)
			res.Counts.Failed++
			continue
		}
		seen[key] = struct{}{}
	}
	res.Pass = res.Counts.Failed == 0
	if !res.Pass {
		res.Message = fmt.Sprintf("pk_unique: %d duplicate or missing-field tuples over fields %v",
			res.Counts.Failed, fields)
	}
	return res
}

func pkKey(r Row, fields []string) (string, bool) {
	parts := make([]string, len(fields))
	for i, f := range fields {
		v, present := r[f]
		if !present || v == nil {
			return "", false
		}
		parts[i] = fmt.Sprintf("%T:%v", v, v)
	}
	return strings.Join(parts, "\x1f"), true
}

func runRowCountRatio(cfg map[string]any, rows []Row) Result {
	baselineRaw, ok := cfg["baseline"]
	if !ok {
		return Result{Kind: "row_count_ratio", Message: "config.baseline required", ConfigError: true}
	}
	baseline, ok := checkspec.Numeric(baselineRaw)
	if !ok || baseline <= 0 {
		return Result{Kind: "row_count_ratio", Message: "config.baseline must be a positive number", ConfigError: true}
	}
	low := 0.5
	if raw, ok := cfg["low"]; ok {
		v, numOK := checkspec.Numeric(raw)
		if !numOK {
			return Result{Kind: "row_count_ratio", Message: "config.low must be numeric", ConfigError: true}
		}
		low = v
	}
	high := 2.0
	if raw, ok := cfg["high"]; ok {
		v, numOK := checkspec.Numeric(raw)
		if !numOK {
			return Result{Kind: "row_count_ratio", Message: "config.high must be numeric", ConfigError: true}
		}
		high = v
	}
	if low < 0 || high < 0 {
		return Result{Kind: "row_count_ratio", Message: "config.low and config.high must be non-negative", ConfigError: true}
	}
	if low > high {
		return Result{Kind: "row_count_ratio",
			Message:     fmt.Sprintf("config.low (%g) must be <= config.high (%g)", low, high),
			ConfigError: true}
	}
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

func runRowCountAbsolute(cfg map[string]any, rows []Row) Result {
	minVal, ok := checkspec.Numeric(cfg["min"])
	if !ok {
		return Result{Kind: "row_count_absolute", Message: "config.min required", ConfigError: true}
	}
	res := Result{Kind: "row_count_absolute", Counts: Counters{Rows: len(rows)}}
	if float64(len(rows)) < minVal {
		res.Message = fmt.Sprintf("row_count_absolute: %d rows < min %g", len(rows), minVal)
		return res
	}
	if maxRaw, ok := cfg["max"]; ok {
		if maxVal, ok := checkspec.Numeric(maxRaw); ok && float64(len(rows)) > maxVal {
			res.Message = fmt.Sprintf("row_count_absolute: %d rows > max %g", len(rows), maxVal)
			return res
		}
	}
	res.Pass = true
	return res
}

func runValueInSet(cfg map[string]any, rows []Row) Result {
	field, _ := cfg["field"].(string)
	if field == "" {
		return Result{Kind: "value_in_set", Message: "config.field required", ConfigError: true}
	}
	set := map[string]struct{}{}
	if vs, ok := cfg["set"].([]any); ok {
		for _, v := range vs {
			set[fmt.Sprintf("%v", v)] = struct{}{}
		}
	}
	if len(set) == 0 {
		return Result{Kind: "value_in_set", Message: "config.set required", ConfigError: true}
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

func runRegexMatch(cfg map[string]any, rows []Row) Result {
	field, _ := cfg["field"].(string)
	if field == "" {
		return Result{Kind: "regex_match", Message: "config.field required", ConfigError: true}
	}
	pattern, _ := cfg["pattern"].(string)
	if pattern == "" {
		return Result{Kind: "regex_match", Message: "config.pattern required", ConfigError: true}
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{Kind: "regex_match", Message: "config.pattern compile: " + err.Error(), ConfigError: true}
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

func runNumericRange(cfg map[string]any, rows []Row) Result {
	field, _ := cfg["field"].(string)
	if field == "" {
		return Result{Kind: "numeric_range", Message: "config.field required", ConfigError: true}
	}
	minRaw, hasMin := cfg["min"]
	maxRaw, hasMax := cfg["max"]
	if !hasMin && !hasMax {
		return Result{Kind: "numeric_range", Message: "config.min or config.max required", ConfigError: true}
	}
	var minBound, maxBound float64
	if hasMin {
		m, ok := checkspec.Numeric(minRaw)
		if !ok {
			return Result{Kind: "numeric_range", Message: fmt.Sprintf("config.min must be numeric, got %v (%T)", minRaw, minRaw), ConfigError: true}
		}
		minBound = m
	}
	if hasMax {
		m, ok := checkspec.Numeric(maxRaw)
		if !ok {
			return Result{Kind: "numeric_range", Message: fmt.Sprintf("config.max must be numeric, got %v (%T)", maxRaw, maxRaw), ConfigError: true}
		}
		maxBound = m
	}
	res := Result{Kind: "numeric_range", Counts: Counters{Rows: len(rows)}}
	for _, r := range rows {
		v, ok := checkspec.Numeric(r[field])
		if !ok {
			res.Failed = appendBounded(res.Failed, r, 100)
			res.Counts.Failed++
			continue
		}
		if hasMin && v < minBound {
			res.Failed = appendBounded(res.Failed, r, 100)
			res.Counts.Failed++
			continue
		}
		if hasMax && v > maxBound {
			res.Failed = appendBounded(res.Failed, r, 100)
			res.Counts.Failed++
		}
	}
	res.Pass = res.Counts.Failed == 0
	if !res.Pass {
		res.Message = fmt.Sprintf("numeric_range: %d/%d rows outside bounds for field %q",
			res.Counts.Failed, res.Counts.Rows, field)
	}
	return res
}

func appendBounded(out []Row, r Row, limit int) []Row {
	if len(out) >= limit {
		return out
	}
	return append(out, r)
}
