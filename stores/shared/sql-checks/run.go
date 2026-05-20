// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package sqlchecks

import (
	"context"
	"fmt"
)

// Conn is the minimal interface needed to run a check. pgx and
// database/sql both fit; the executor wraps its connection at the
// caller boundary so this package can stay backend-agnostic.
type Conn interface {
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
}

// Rows is the minimal cursor interface needed by Run. Mirrors the
// pgx.Rows / database/sql.Rows surface.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}

// Run compiles and executes each check against conn, returning the
// per-check results in spec order. The first compile error short-
// circuits (no checks are run). Per-check execution errors surface in
// the corresponding Result.Message with Pass=false rather than
// aborting the whole batch — operators get a coherent verifier
// terminal even when one check's substrate-side call fails.
//
// Per spec §Item 6.
func Run(ctx context.Context, conn Conn, schema, table string, specs []CheckSpec) ([]Result, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("sqlchecks.Run: at least one CheckSpec required")
	}
	// Pre-compile so a malformed spec fails fast before any substrate
	// roundtrip.
	compiled := make([]Compiled, 0, len(specs))
	for i, s := range specs {
		c, err := Compile(s, schema, table)
		if err != nil {
			return nil, fmt.Errorf("checks[%d] (%s): %w", i, s.Kind, err)
		}
		compiled = append(compiled, c)
	}
	out := make([]Result, 0, len(compiled))
	for _, c := range compiled {
		out = append(out, runOne(ctx, conn, c))
	}
	return out, nil
}

// runOne executes a single Compiled check and runs its Interpret.
func runOne(ctx context.Context, conn Conn, c Compiled) Result {
	rows, err := conn.Query(ctx, c.SQL)
	if err != nil {
		return Result{Kind: c.Kind, Message: fmt.Sprintf("query failed: %v", err)}
	}
	defer rows.Close()

	switch c.Kind {
	case "pk_unique":
		// pk_unique surfaces a row-existence signal. Any row → fail.
		hasRow := rows.Next()
		if err := rows.Err(); err != nil {
			return Result{Kind: c.Kind, Message: fmt.Sprintf("scan failed: %v", err)}
		}
		return c.Interpret(hasRow)
	default:
		// Single-row aggregate kinds (no_nulls, row_count_absolute).
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return Result{Kind: c.Kind, Message: fmt.Sprintf("scan failed: %v", err)}
			}
			return Result{Kind: c.Kind, Message: "query returned no rows"}
		}
		var val any
		if err := rows.Scan(&val); err != nil {
			return Result{Kind: c.Kind, Message: fmt.Sprintf("scan failed: %v", err)}
		}
		return c.Interpret(val)
	}
}
