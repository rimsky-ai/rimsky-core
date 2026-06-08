// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlchecks

import (
	"fmt"
	"regexp"
	"strings"
)

// identRegex bounds SQL identifiers we'll splice into the generated
// query. Matches the convention used in stores/postgres/store
// (`[a-z_][a-z0-9_]*`): lowercase letters, digits, underscores; not
// starting with a digit. Validated at compile time so callers cannot
// punch through with a tampered attribute payload.
var identRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// selectOnlyRegex is the belt-and-suspenders SELECT-only enforcement.
// The kind compilers produce SELECT-only SQL by construction, but the
// check pins the invariant for the test suite (and serves as the
// rejection gate if a future compiler regression slips through). Per
// spec §Item 6 — Side-effect discipline.
var selectOnlyRegex = regexp.MustCompile(`(?i)^\s*SELECT\s`)

// Compiled is one check ready to execute against a substrate.
type Compiled struct {
	// Kind is the check kind, echoed into the Result by Interpret.
	Kind string
	// SQL is the aggregate-only query for the check; the executor runs
	// it via Conn.Query.
	SQL string
	// Interpret turns the scanned values into a Result. Each check
	// kind has its own interpreter shape; see the per-kind compilers
	// for the scan slot the interpreter expects.
	Interpret func(scanned ...any) Result
}

// Compile takes a check spec plus the schema + table names the check
// will run against, and returns a Compiled query and a per-kind result
// interpreter. Schema and table names are validated as SQL identifiers
// (lowercase letters, digits, underscores; not starting with a digit).
//
// Supported kinds (v1): no_nulls, row_count_absolute, row_count_ratio,
// pk_unique.
//
// Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6 — Check vocabulary.
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

// compileNoNulls emits an aggregate-only NULL count over the named
// columns and a fail-on-positive interpreter. The optional `threshold`
// config (default 0) allows non-zero tolerance — the SQL-side
// extension of the in-process shape's no_nulls.
func compileNoNulls(cfg map[string]any, schema, table string) (Compiled, error) {
	fields, err := fieldList(cfg)
	if err != nil {
		return Compiled{}, err
	}
	for _, f := range fields {
		if !identRegex.MatchString(f) {
			return Compiled{}, fmt.Errorf("no_nulls: invalid column identifier %q", f)
		}
	}
	threshold, _ := numericDefault(cfg["threshold"], 0)
	terms := make([]string, len(fields))
	for i, f := range fields {
		terms[i] = fmt.Sprintf("count(*) FILTER (WHERE %s IS NULL)", f)
	}
	sql := fmt.Sprintf("SELECT %s FROM %s.%s", strings.Join(terms, " + "), schema, table)
	thresholdLocal := threshold
	fieldsLocal := append([]string(nil), fields...)
	return Compiled{
		Kind: "no_nulls",
		SQL:  sql,
		Interpret: func(scanned ...any) Result {
			if len(scanned) == 0 {
				return Result{Kind: "no_nulls", Message: "scan produced no values"}
			}
			n, ok := numeric(scanned[0])
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

// compileRowCountAbsolute emits `SELECT count(*) FROM s.t` and an
// interpreter that fails when the count falls outside the configured
// `[min, max?]` bounds.
func compileRowCountAbsolute(cfg map[string]any, schema, table string) (Compiled, error) {
	minVal, ok := numeric(cfg["min"])
	if !ok {
		return Compiled{}, fmt.Errorf("row_count_absolute: config.min required (numeric)")
	}
	maxRaw, hasMax := cfg["max"]
	var maxVal float64
	if hasMax {
		mv, ok := numeric(maxRaw)
		if !ok {
			return Compiled{}, fmt.Errorf("row_count_absolute: config.max must be numeric when set")
		}
		maxVal = mv
	}
	sql := fmt.Sprintf("SELECT count(*) FROM %s.%s", schema, table)
	return Compiled{
		Kind: "row_count_absolute",
		SQL:  sql,
		Interpret: func(scanned ...any) Result {
			if len(scanned) == 0 {
				return Result{Kind: "row_count_absolute", Message: "scan produced no values"}
			}
			n, ok := numeric(scanned[0])
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

// compileRowCountRatio emits `SELECT count(*) FROM s.t` and an
// interpreter that computes `ratio = row_count / baseline` and fails
// when the ratio falls outside `[low, high]`. The SQL-side mirror of the
// in-process shape's runRowCountRatio: same config vocabulary
// (`baseline` required and > 0; `low` default 0.5; `high` default 2.0)
// so an operator can move a check between the two substrates without
// retranslating its config. The query is aggregate-only and
// SELECT-prefixed — count(*) only, no row scan — to hold the
// side-effect-free / SELECT-only discipline the compiler enforces.
func compileRowCountRatio(cfg map[string]any, schema, table string) (Compiled, error) {
	baselineRaw, hasBaseline := cfg["baseline"]
	if !hasBaseline {
		return Compiled{}, fmt.Errorf("row_count_ratio: config.baseline required (numeric)")
	}
	baseline, ok := numeric(baselineRaw)
	if !ok || baseline <= 0 {
		return Compiled{}, fmt.Errorf("row_count_ratio: config.baseline must be a positive number")
	}
	low, _ := numericDefault(cfg["low"], 0.5)
	high, _ := numericDefault(cfg["high"], 2.0)
	sql := fmt.Sprintf("SELECT count(*) FROM %s.%s", schema, table)
	return Compiled{
		Kind: "row_count_ratio",
		SQL:  sql,
		Interpret: func(scanned ...any) Result {
			if len(scanned) == 0 {
				return Result{Kind: "row_count_ratio", Message: "scan produced no values"}
			}
			n, ok := numeric(scanned[0])
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

// compilePKUnique emits a `SELECT c1,...,cN, count(*) FROM s.t GROUP BY
// c1,...,cN HAVING count(*) > 1 LIMIT 1`; any returned row is a
// uniqueness violation.
func compilePKUnique(cfg map[string]any, schema, table string) (Compiled, error) {
	fields, err := fieldList(cfg)
	if err != nil {
		return Compiled{}, err
	}
	for _, f := range fields {
		if !identRegex.MatchString(f) {
			return Compiled{}, fmt.Errorf("pk_unique: invalid column identifier %q", f)
		}
	}
	colList := strings.Join(fields, ", ")
	sql := fmt.Sprintf(
		"SELECT %s, count(*) FROM %s.%s GROUP BY %s HAVING count(*) > 1 LIMIT 1",
		colList, schema, table, colList,
	)
	fieldsLocal := append([]string(nil), fields...)
	return Compiled{
		Kind: "pk_unique",
		SQL:  sql,
		// Interpret receives ok=true → row was scanned (at least one
		// duplicate exists, fail). ok=false → no rows, pass.
		Interpret: func(scanned ...any) Result {
			// The scanned slot here is a single bool: did a row return?
			// (The runner converts query.Next() into this signal.)
			res := Result{
				Kind:   "pk_unique",
				Counts: map[string]any{"fields": fieldsLocal},
			}
			if len(scanned) == 0 {
				// No row returned → no duplicates → pass.
				res.Pass = true
				return res
			}
			any, ok := scanned[0].(bool)
			if !ok || !any {
				res.Pass = true
				return res
			}
			res.Message = fmt.Sprintf("pk_unique: duplicate tuples found over fields %v", fieldsLocal)
			return res
		},
	}, nil
}

// fieldList parses a `field` (string) or `fields` ([]string) config
// entry into a normalized slice. Mirrors the verifier-shape-checks
// helper so the two vocabularies accept the same attribute shape.
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
	if vs, ok := cfg["fields"].([]string); ok {
		return append([]string(nil), vs...), nil
	}
	return nil, fmt.Errorf("config: `field` (string) or `fields` ([]string) required")
}

// numeric mirrors the verifier-shape-checks helper: float64 for any
// numeric JSON / Go scalar; second return false when not numeric.
func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

func numericDefault(v any, def float64) (float64, bool) {
	if x, ok := numeric(v); ok {
		return x, true
	}
	return def, false
}
