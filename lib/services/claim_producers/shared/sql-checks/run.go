// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlchecks

import (
	"context"
	"fmt"
)

type Conn interface {
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
}

type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}

func Run(ctx context.Context, conn Conn, schema, table string, specs []CheckSpec) ([]Result, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("sqlchecks.Run: at least one CheckSpec required")
	}
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

func runOne(ctx context.Context, conn Conn, c Compiled) Result {
	rows, err := conn.Query(ctx, c.SQL)
	if err != nil {
		return Result{Kind: c.Kind, Error: fmt.Sprintf("query failed: %v", err)}
	}
	defer rows.Close()

	switch c.Kind {
	case "pk_unique":
		hasRow := rows.Next()
		if err := rows.Err(); err != nil {
			return Result{Kind: c.Kind, Error: fmt.Sprintf("scan failed: %v", err)}
		}
		return c.Interpret(hasRow)
	default:
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return Result{Kind: c.Kind, Error: fmt.Sprintf("scan failed: %v", err)}
			}
			return Result{Kind: c.Kind, Message: "query returned no rows"}
		}
		var val any
		if err := rows.Scan(&val); err != nil {
			return Result{Kind: c.Kind, Error: fmt.Sprintf("scan failed: %v", err)}
		}
		return c.Interpret(val)
	}
}
