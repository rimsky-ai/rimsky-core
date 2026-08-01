// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlchecks

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/checkspec"
)

var identRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

var selectOnlyRegex = regexp.MustCompile(`(?i)^\s*SELECT\s`)

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteQualified(schema, table string) string {
	return quoteIdent(schema) + "." + quoteIdent(table)
}

func numericConfigOrDefault(cfg map[string]any, key string, def float64) (float64, error) {
	raw, present := cfg[key]
	if !present || raw == nil {
		return def, nil
	}
	v, ok := checkspec.Numeric(raw)
	if !ok {
		return 0, fmt.Errorf("config.%s must be numeric, got %v", key, raw)
	}
	return v, nil
}

type Compiled struct {
	Kind      string
	SQL       string
	Interpret func(scanned ...any) Result
}

func Compile(spec CheckSpec, schema, table string) (Compiled, error) {
	if !identRegex.MatchString(schema) {
		return Compiled{}, fmt.Errorf("invalid schema identifier %q", schema)
	}
	if !identRegex.MatchString(table) {
		return Compiled{}, fmt.Errorf("invalid table identifier %q", table)
	}
	var (
		out Compiled
		err error
	)
	switch spec.Kind {
	case "no_nulls":
		out, err = compileNoNulls(spec.Config, schema, table)
	case "row_count_absolute":
		out, err = compileRowCountAbsolute(spec.Config, schema, table)
	case "row_count_ratio":
		out, err = compileRowCountRatio(spec.Config, schema, table)
	case "pk_unique":
		out, err = compilePKUnique(spec.Config, schema, table)
	default:
		return Compiled{}, fmt.Errorf("unknown check kind %q", spec.Kind)
	}
	if err != nil {
		return Compiled{}, err
	}
	if !selectOnlyRegex.MatchString(out.SQL) {
		return Compiled{}, fmt.Errorf("internal: compiled SQL for %q is not SELECT-only", spec.Kind)
	}
	return out, nil
}

func compileNoNulls(cfg map[string]any, schema, table string) (Compiled, error) {
	fields, err := checkspec.FieldList(cfg)
	if err != nil {
		return Compiled{}, err
	}
	for _, f := range fields {
		if !identRegex.MatchString(f) {
			return Compiled{}, fmt.Errorf("no_nulls: invalid column identifier %q", f)
		}
	}
	threshold, err := numericConfigOrDefault(cfg, "threshold", 0)
	if err != nil {
		return Compiled{}, fmt.Errorf("no_nulls: %w", err)
	}
	terms := make([]string, len(fields))
	for i, f := range fields {
		terms[i] = fmt.Sprintf("count(*) FILTER (WHERE %s IS NULL)", quoteIdent(f))
	}
	sql := fmt.Sprintf("SELECT %s FROM %s", strings.Join(terms, " + "), quoteQualified(schema, table))
	thresholdLocal := threshold
	fieldsLocal := make([]any, len(fields))
	for i, f := range fields {
		fieldsLocal[i] = f
	}
	return Compiled{
		Kind: "no_nulls",
		SQL:  sql,
		Interpret: func(scanned ...any) Result {
			if len(scanned) == 0 {
				return Result{Kind: "no_nulls", Message: "scan produced no values"}
			}
			n, ok := checkspec.Numeric(scanned[0])
			if !ok {
				return Result{Kind: "no_nulls", Message: fmt.Sprintf("scan produced non-numeric value %v", scanned[0])}
			}
			res := Result{
				Kind:   "no_nulls",
				Counts: map[string]any{"null_count": n, "threshold": thresholdLocal, "fields": fieldsLocal},
			}
			if n > thresholdLocal {
				res.Message = fmt.Sprintf("no_nulls: %d null values across fields %v exceeds threshold %g", int64(n), fieldsLocal, thresholdLocal)
				return res
			}
			res.Pass = true
			return res
		},
	}, nil
}

func compileRowCountAbsolute(cfg map[string]any, schema, table string) (Compiled, error) {
	minVal, ok := checkspec.Numeric(cfg["min"])
	if !ok {
		return Compiled{}, fmt.Errorf("row_count_absolute: config.min required (numeric)")
	}
	maxRaw, hasMax := cfg["max"]
	var maxVal float64
	if hasMax {
		mv, ok := checkspec.Numeric(maxRaw)
		if !ok {
			return Compiled{}, fmt.Errorf("row_count_absolute: config.max must be numeric when set")
		}
		maxVal = mv
	}
	sql := fmt.Sprintf("SELECT count(*) FROM %s", quoteQualified(schema, table))
	return Compiled{
		Kind: "row_count_absolute",
		SQL:  sql,
		Interpret: func(scanned ...any) Result {
			if len(scanned) == 0 {
				return Result{Kind: "row_count_absolute", Message: "scan produced no values"}
			}
			n, ok := checkspec.Numeric(scanned[0])
			if !ok {
				return Result{Kind: "row_count_absolute", Message: fmt.Sprintf("scan produced non-numeric value %v", scanned[0])}
			}
			res := Result{
				Kind:   "row_count_absolute",
				Counts: map[string]any{"row_count": n, "min": minVal},
			}
			if hasMax {
				res.Counts["max"] = maxVal
			}
			if n < minVal {
				res.Message = fmt.Sprintf("row_count_absolute: %g rows < min %g", n, minVal)
				return res
			}
			if hasMax && n > maxVal {
				res.Message = fmt.Sprintf("row_count_absolute: %g rows > max %g", n, maxVal)
				return res
			}
			res.Pass = true
			return res
		},
	}, nil
}

func compileRowCountRatio(cfg map[string]any, schema, table string) (Compiled, error) {
	baselineRaw, hasBaseline := cfg["baseline"]
	if !hasBaseline {
		return Compiled{}, fmt.Errorf("row_count_ratio: config.baseline required (numeric)")
	}
	baseline, ok := checkspec.Numeric(baselineRaw)
	if !ok || baseline <= 0 {
		return Compiled{}, fmt.Errorf("row_count_ratio: config.baseline must be a positive number")
	}
	low, err := numericConfigOrDefault(cfg, "low", 0.5)
	if err != nil {
		return Compiled{}, fmt.Errorf("row_count_ratio: %w", err)
	}
	high, err := numericConfigOrDefault(cfg, "high", 2.0)
	if err != nil {
		return Compiled{}, fmt.Errorf("row_count_ratio: %w", err)
	}
	if low < 0 {
		return Compiled{}, fmt.Errorf("row_count_ratio: config.low must be >= 0, got %g", low)
	}
	if high < 0 {
		return Compiled{}, fmt.Errorf("row_count_ratio: config.high must be >= 0, got %g", high)
	}
	if low > high {
		return Compiled{}, fmt.Errorf("row_count_ratio: config.low (%g) must be <= config.high (%g); as configured, every ratio would fail", low, high)
	}
	sql := fmt.Sprintf("SELECT count(*) FROM %s", quoteQualified(schema, table))
	return Compiled{
		Kind: "row_count_ratio",
		SQL:  sql,
		Interpret: func(scanned ...any) Result {
			if len(scanned) == 0 {
				return Result{Kind: "row_count_ratio", Message: "scan produced no values"}
			}
			n, ok := checkspec.Numeric(scanned[0])
			if !ok {
				return Result{Kind: "row_count_ratio", Message: fmt.Sprintf("scan produced non-numeric value %v", scanned[0])}
			}
			ratio := n / baseline
			res := Result{
				Kind: "row_count_ratio",
				Counts: map[string]any{
					"row_count": n,
					"baseline":  baseline,
					"low":       low,
					"high":      high,
					"ratio":     ratio,
				},
			}
			if ratio < low || ratio > high {
				res.Message = fmt.Sprintf("row_count_ratio: ratio=%.2f outside [%g, %g] (rows=%g, baseline=%g)",
					ratio, low, high, n, baseline)
				return res
			}
			res.Pass = true
			return res
		},
	}, nil
}

func compilePKUnique(cfg map[string]any, schema, table string) (Compiled, error) {
	fields, err := checkspec.FieldList(cfg)
	if err != nil {
		return Compiled{}, err
	}
	for _, f := range fields {
		if !identRegex.MatchString(f) {
			return Compiled{}, fmt.Errorf("pk_unique: invalid column identifier %q", f)
		}
	}
	quotedFields := make([]string, len(fields))
	for i, f := range fields {
		quotedFields[i] = quoteIdent(f)
	}
	colList := strings.Join(quotedFields, ", ")
	sql := fmt.Sprintf(
		"SELECT %s, count(*) FROM %s GROUP BY %s HAVING count(*) > 1 LIMIT 1",
		colList, quoteQualified(schema, table), colList,
	)
	fieldsLocal := make([]any, len(fields))
	for i, f := range fields {
		fieldsLocal[i] = f
	}
	return Compiled{
		Kind: "pk_unique",
		SQL:  sql,
		Interpret: func(scanned ...any) Result {
			res := Result{
				Kind:   "pk_unique",
				Counts: map[string]any{"fields": fieldsLocal},
			}
			if len(scanned) != 1 {
				res.Message = fmt.Sprintf("pk_unique: scan produced %d values, want exactly 1 (a duplicate-exists bool)", len(scanned))
				return res
			}
			dup, ok := scanned[0].(bool)
			if !ok {
				res.Message = fmt.Sprintf("pk_unique: scan produced non-bool value %v", scanned[0])
				return res
			}
			if dup {
				res.Message = fmt.Sprintf("pk_unique: duplicate tuples found over fields %v", fieldsLocal)
				return res
			}
			res.Pass = true
			return res
		},
	}, nil
}
