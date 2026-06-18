// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlchecks

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCompile_NoNulls(t *testing.T) {
	t.Run("single field", func(t *testing.T) {
		c, err := Compile(CheckSpec{Kind: "no_nulls", Config: map[string]any{"fields": []any{"id"}}}, "staging_x", "items")
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		want := "SELECT count(*) FILTER (WHERE id IS NULL) FROM staging_x.items"
		if c.SQL != want {
			t.Fatalf("SQL mismatch:\nwant %q\ngot  %q", want, c.SQL)
		}
	})

	t.Run("multiple fields", func(t *testing.T) {
		c, err := Compile(CheckSpec{Kind: "no_nulls", Config: map[string]any{"fields": []any{"id", "payload"}}}, "s", "t")
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		want := "SELECT count(*) FILTER (WHERE id IS NULL) + count(*) FILTER (WHERE payload IS NULL) FROM s.t"
		if c.SQL != want {
			t.Fatalf("SQL mismatch:\nwant %q\ngot  %q", want, c.SQL)
		}
	})

	t.Run("interpret pass (count=0)", func(t *testing.T) {
		c, _ := Compile(CheckSpec{Kind: "no_nulls", Config: map[string]any{"fields": []any{"id"}}}, "s", "t")
		res := c.Interpret(int64(0))
		if !res.Pass {
			t.Fatalf("expected pass, got: %+v", res)
		}
	})

	t.Run("interpret fail (count=3)", func(t *testing.T) {
		c, _ := Compile(CheckSpec{Kind: "no_nulls", Config: map[string]any{"fields": []any{"id"}}}, "s", "t")
		res := c.Interpret(int64(3))
		if res.Pass {
			t.Fatalf("expected fail")
		}
		if !strings.Contains(res.Message, "exceeds threshold") {
			t.Fatalf("message %q", res.Message)
		}
	})

	t.Run("threshold tolerance accepts non-zero", func(t *testing.T) {
		c, _ := Compile(CheckSpec{Kind: "no_nulls", Config: map[string]any{"fields": []any{"id"}, "threshold": 5}}, "s", "t")
		if res := c.Interpret(int64(5)); !res.Pass {
			t.Fatalf("expected pass at threshold, got %+v", res)
		}
		if res := c.Interpret(int64(6)); res.Pass {
			t.Fatalf("expected fail above threshold")
		}
	})
}

func TestCompile_RowCountAbsolute(t *testing.T) {
	t.Run("min only", func(t *testing.T) {
		c, err := Compile(CheckSpec{Kind: "row_count_absolute", Config: map[string]any{"min": 1000}}, "s", "t")
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		if c.SQL != "SELECT count(*) FROM s.t" {
			t.Fatalf("got SQL %q", c.SQL)
		}
		if res := c.Interpret(int64(999)); res.Pass {
			t.Fatalf("expected fail at 999 < min 1000")
		}
		if res := c.Interpret(int64(1000)); !res.Pass {
			t.Fatalf("expected pass at 1000 >= min 1000, got %+v", res)
		}
	})

	t.Run("min and max", func(t *testing.T) {
		c, _ := Compile(CheckSpec{Kind: "row_count_absolute", Config: map[string]any{"min": 1000, "max": 2000}}, "s", "t")
		if res := c.Interpret(int64(2500)); res.Pass {
			t.Fatalf("expected fail at 2500 > max 2000")
		}
		if res := c.Interpret(int64(1500)); !res.Pass {
			t.Fatalf("expected pass at 1500 in [1000,2000], got %+v", res)
		}
	})

	t.Run("missing min rejected", func(t *testing.T) {
		_, err := Compile(CheckSpec{Kind: "row_count_absolute", Config: map[string]any{}}, "s", "t")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestCompile_PKUnique(t *testing.T) {
	t.Run("single field", func(t *testing.T) {
		c, err := Compile(CheckSpec{Kind: "pk_unique", Config: map[string]any{"fields": []any{"id"}}}, "s", "t")
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		want := "SELECT id, count(*) FROM s.t GROUP BY id HAVING count(*) > 1 LIMIT 1"
		if c.SQL != want {
			t.Fatalf("SQL:\nwant %q\ngot  %q", want, c.SQL)
		}
	})

	t.Run("multi-field composite", func(t *testing.T) {
		c, _ := Compile(CheckSpec{Kind: "pk_unique", Config: map[string]any{"fields": []any{"id", "version"}}}, "s", "t")
		want := "SELECT id, version, count(*) FROM s.t GROUP BY id, version HAVING count(*) > 1 LIMIT 1"
		if c.SQL != want {
			t.Fatalf("SQL:\nwant %q\ngot  %q", want, c.SQL)
		}
	})

	t.Run("interpret zero rows scanned → pass", func(t *testing.T) {
		c, _ := Compile(CheckSpec{Kind: "pk_unique", Config: map[string]any{"fields": []any{"id"}}}, "s", "t")
		res := c.Interpret(false)
		if !res.Pass {
			t.Fatalf("expected pass when no duplicate, got %+v", res)
		}
	})

	t.Run("interpret row exists → fail", func(t *testing.T) {
		c, _ := Compile(CheckSpec{Kind: "pk_unique", Config: map[string]any{"fields": []any{"id"}}}, "s", "t")
		res := c.Interpret(true)
		if res.Pass {
			t.Fatal("expected fail when duplicate row present")
		}
	})
}

func TestCompile_InvalidIdentifiers(t *testing.T) {
	tests := []struct {
		schema, table string
	}{
		{"BadCase", "items"},
		{"s", "ITEMS"},
		{"", "items"},
		{"s", ""},
		{"123start", "items"},
		{"s", "with-dash"},
		{"s; DROP TABLE", "items"},
	}
	for _, tc := range tests {
		_, err := Compile(CheckSpec{Kind: "row_count_absolute", Config: map[string]any{"min": 1}}, tc.schema, tc.table)
		if err == nil {
			t.Fatalf("expected error for schema=%q table=%q", tc.schema, tc.table)
		}
	}
}

func TestCompile_InvalidColumnIdentifiers(t *testing.T) {
	_, err := Compile(CheckSpec{Kind: "no_nulls", Config: map[string]any{"fields": []any{"id; DROP TABLE"}}}, "s", "t")
	if err == nil {
		t.Fatal("expected error for malformed column name")
	}
}

func TestCompile_UnknownKind(t *testing.T) {
	_, err := Compile(CheckSpec{Kind: "made_up", Config: map[string]any{}}, "s", "t")
	if err == nil {
		t.Fatal("expected unknown-kind error")
	}
}

type fakeRows struct {
	values [][]any
	idx    int
	closed bool
}

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.values) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.values) {
		return errors.New("scan called out of order")
	}
	row := r.values[r.idx-1]
	for i := range dest {
		if i >= len(row) {
			break
		}
		switch d := dest[i].(type) {
		case *any:
			*d = row[i]
		}
	}
	return nil
}

func (r *fakeRows) Close()     { r.closed = true }
func (r *fakeRows) Err() error { return nil }

type fakeConn struct {
	responses map[string][][]any
}

func (c *fakeConn) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	vals, ok := c.responses[sql]
	if !ok {
		return &fakeRows{}, nil
	}
	return &fakeRows{values: vals}, nil
}

func TestRun_AllPass(t *testing.T) {
	conn := &fakeConn{responses: map[string][][]any{
		"SELECT count(*) FROM s.t":                                             {{int64(1500)}},
		"SELECT count(*) FILTER (WHERE id IS NULL) FROM s.t":                   {{int64(0)}},
		"SELECT id, count(*) FROM s.t GROUP BY id HAVING count(*) > 1 LIMIT 1": nil,
	}}
	specs := []CheckSpec{
		{Kind: "row_count_absolute", Config: map[string]any{"min": 1000}},
		{Kind: "no_nulls", Config: map[string]any{"fields": []any{"id"}}},
		{Kind: "pk_unique", Config: map[string]any{"fields": []any{"id"}}},
	}
	results, err := Run(context.Background(), conn, "s", "t", specs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, r := range results {
		if !r.Pass {
			t.Fatalf("expected all pass, got: %+v", r)
		}
	}
}

func TestRun_AnyFailFails(t *testing.T) {
	conn := &fakeConn{responses: map[string][][]any{
		"SELECT count(*) FROM s.t": {{int64(999)}},
	}}
	specs := []CheckSpec{
		{Kind: "row_count_absolute", Config: map[string]any{"min": 1000}},
	}
	results, _ := Run(context.Background(), conn, "s", "t", specs)
	if results[0].Pass {
		t.Fatalf("expected fail, got %+v", results[0])
	}
}

func TestRun_EmptySpecsRejected(t *testing.T) {
	_, err := Run(context.Background(), &fakeConn{}, "s", "t", nil)
	if err == nil {
		t.Fatal("expected error for empty specs")
	}
}

func TestSelectOnlyEnforcement(t *testing.T) {
	sqls := []string{
		"DROP TABLE s.t",
		"INSERT INTO s.t VALUES (1)",
		"  -- comment\nSELECT count(*) FROM s.t",
	}
	for _, sql := range sqls {
		if selectOnlyRegex.MatchString(sql) {
			t.Fatalf("regex should reject %q", sql)
		}
	}
	if !selectOnlyRegex.MatchString("SELECT count(*) FROM s.t") {
		t.Fatal("regex should accept SELECT")
	}
}
