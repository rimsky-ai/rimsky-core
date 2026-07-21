// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var expectedColumns = []struct {
	name     string
	dataType string
}{
	{"item_id", "text"},
	{"payload", "jsonb"},
	{"state", "text"},
	{"claim_token", "text"},
	{"claimed_at", "timestamp with time zone"},
	{"enqueued_at", "timestamp with time zone"},
	{"priority", "integer"},
	{"sequence", "bigint"},
}

func verifyTableExists(ctx context.Context, pool *pgxpool.Pool, table string) error {
	var found bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			 WHERE table_schema = current_schema() AND table_name = $1
		)`,
		table,
	).Scan(&found)
	if err != nil {
		return fmt.Errorf("query information_schema.tables: %w", err)
	}
	if !found {
		return fmt.Errorf("table not found in current schema")
	}
	return nil
}

func verifyItemsTable(ctx context.Context, pool *pgxpool.Pool, table string) error {
	rows, err := pool.Query(ctx,
		`SELECT column_name, data_type
		   FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = $1`,
		table,
	)
	if err != nil {
		return fmt.Errorf("query information_schema.columns: %w", err)
	}
	defer rows.Close()

	got := make(map[string]string)
	for rows.Next() {
		var col, typ string
		if err := rows.Scan(&col, &typ); err != nil {
			return fmt.Errorf("scan column row: %w", err)
		}
		got[col] = typ
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate column rows: %w", err)
	}
	if len(got) == 0 {
		return fmt.Errorf("table not found in current schema (or has zero columns)")
	}
	for _, want := range expectedColumns {
		gotType, ok := got[want.name]
		if !ok {
			return fmt.Errorf("missing column %q (expected type %q)", want.name, want.dataType)
		}
		if gotType != want.dataType {
			return fmt.Errorf("column %q has type %q, expected %q", want.name, gotType, want.dataType)
		}
	}
	return nil
}
