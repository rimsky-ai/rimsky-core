// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pgpool_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pgpool"
)

func TestPoolClonesFromTemplateAndIsolatesAcquires(t *testing.T) {
	ctx := context.Background()
	p := pgpool.New(pgpool.Config{
		Image:    "postgres:14-alpine",
		Database: "pgpooltest",
		User:     "pgpooltest",
		Password: "pgpooltest",
		InitTemplate: func(ctx context.Context, dsn string) error {
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return err
			}
			defer pool.Close()
			_, err = pool.Exec(ctx, `CREATE TABLE seeded(v INT)`)
			if err != nil {
				return err
			}
			_, err = pool.Exec(ctx, `INSERT INTO seeded VALUES (42)`)
			return err
		},
	})

	dsnA := p.Acquire(ctx, t)
	dsnB := p.Acquire(ctx, t)
	dsnEmpty := p.AcquireFresh(ctx, t)

	if dsnA == dsnB {
		t.Fatalf("Acquire returned identical DSNs: %s", dsnA)
	}

	connect := func(dsn string) *pgxpool.Pool {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("connect %s: %v", dsn, err)
		}
		t.Cleanup(pool.Close)
		return pool
	}

	poolA := connect(dsnA)
	poolB := connect(dsnB)
	poolEmpty := connect(dsnEmpty)

	var v int
	if err := poolA.QueryRow(ctx, `SELECT v FROM seeded`).Scan(&v); err != nil {
		t.Fatalf("poolA template read: %v", err)
	}
	if v != 42 {
		t.Fatalf("poolA seeded row: got %d want 42", v)
	}

	if _, err := poolA.Exec(ctx, `INSERT INTO seeded VALUES (99)`); err != nil {
		t.Fatalf("poolA insert: %v", err)
	}

	var countB int
	if err := poolB.QueryRow(ctx, `SELECT COUNT(*) FROM seeded`).Scan(&countB); err != nil {
		t.Fatalf("poolB read: %v", err)
	}
	if countB != 1 {
		t.Fatalf("poolB count after poolA insert: got %d want 1 (clones must be isolated)", countB)
	}

	var has bool
	err := poolEmpty.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='seeded')`,
	).Scan(&has)
	if err != nil {
		t.Fatalf("poolEmpty introspect: %v", err)
	}
	if has {
		t.Fatalf("AcquireFresh returned a DB that carries template tables")
	}
}
