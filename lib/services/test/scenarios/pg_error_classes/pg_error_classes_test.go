// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pgerrorclasses

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

const queueItemsTable = "items_unavailable"

const queueSelector = "@queue"

const canonicalSchema = "production_swap_collision"

func TestPGErrorClasses_Delivered(t *testing.T) {
	t.Parallel()
	t.Run("claim_unavailable", testClaimUnavailableDelivered)
	t.Run("not_atomically_replaceable", testNotReplaceableDelivered)
}

func testClaimUnavailableDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	substrate := harness.StartPostgresOnNetwork(ctx, t, netName, "substrate-pg")
	pool := dialSubstrate(ctx, t, substrate.HostDSN)

	createItemsTable(t, pool, queueItemsTable)

	producerEndpoint := harness.StartPostgresClaimProducer(ctx, t, netName, "claim-producer-postgres", harness.PostgresClaimProducerSpec{
		Connection:     substrate.InternalDSN,
		WriteSemantics: "sync",
		PickPolicies: map[string]harness.PostgresPickPolicy{
			queueSelector: {
				ItemsTable:               queueItemsTable,
				OnCommit:                 "pop",
				OnGiveUp:                 "recycle",
				VisibilityTimeoutSeconds: 1800,
			},
		},
	})

	execEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("queue-store", producerEndpoint, "sync"),
		harness.WithExecutor("stub", execEndpoint),
	)

	templateID := ep.DeployTemplate(t, map[string]any{
		"spec": map[string]any{
			"name":    "pg-claim-unavailable",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{
							"name":     "queue-store",
							"selector": queueSelector,
							"intent":   "rw",
							"alias":    "work",
						},
					},
				},
				{
					"type":     "subscriber",
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"node": "worker", "type": "terminal/error/*",
							"force_upstream_refresh": false,
						},
					},
				},
			},
		},
	})

	instanceID := ep.CreateInstance(t, templateID, "ck-pg-claim-unavailable", "pg-error-classes")

	requireEventKind(t, ep, instanceID,
		"terminal/error/pg/claim_unavailable",
		"the empty pick-policy items table must deliver the producer-declared "+
			"pg/claim_unavailable class as a real signal, not only the synthetic "+
			"acquire/unavailable")
}

func testNotReplaceableDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	substrate := harness.StartPostgresOnNetwork(ctx, t, netName, "substrate-pg")
	pool := dialSubstrate(ctx, t, substrate.HostDSN)

	seedSwapCollision(t, pool, canonicalSchema)

	producerEndpoint := harness.StartPostgresClaimProducer(ctx, t, netName, "claim-producer-postgres", harness.PostgresClaimProducerSpec{
		Connection:     substrate.InternalDSN,
		WriteSemantics: "staged_async",
		EnableExecutor: true,
	})

	execEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("staged-store", producerEndpoint, "staged_async"),
		harness.WithExecutor("stub", execEndpoint),
	)

	templateID := ep.DeployTemplate(t, map[string]any{
		"spec": map[string]any{
			"name":    "pg-swap-failed",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "acquirer",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{
							"name":     "staged-store",
							"selector": canonicalSchema,
							"intent":   "rw",
							"alias":    "held",
						},
					},
				},
				{
					"type":     "verifier",
					"executor": "stub",
					"holds": map[string]any{
						"held": map[string]any{"from": "acquirer"},
					},
					"subscribes": []map[string]any{
						{"node": "acquirer", "type": "terminal/*", "force_upstream_refresh": false},
					},
				},
				{
					"type":     "subscriber",
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"node": "acquirer", "type": "terminal/error/*",
							"force_upstream_refresh": false,
						},
					},
				},
			},
		},
	})

	instanceID := ep.CreateInstance(t, templateID, "ck-pg-not-replaceable", "pg-error-classes")

	requireEventKind(t, ep, instanceID,
		"terminal/error/pg/not_atomically_replaceable",
		"a write-intent Open on a canonical with an external cross-schema dependent must "+
			"fail fast at Open and deliver the producer-declared pg/not_atomically_replaceable "+
			"class as a real signal, never staging then cascade-destroying the dependent")

	requireNoStagingAndDependentsIntact(t, pool, canonicalSchema)
}

func dialSubstrate(ctx context.Context, t *testing.T, hostDSN string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, hostDSN)
	if err != nil {
		t.Fatalf("dial substrate pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func createItemsTable(t *testing.T, pool *pgxpool.Pool, table string) {
	t.Helper()
	stmt := fmt.Sprintf(`
		CREATE TABLE %s (
			item_id     TEXT PRIMARY KEY,
			payload     JSONB NOT NULL,
			state       TEXT NOT NULL DEFAULT 'available',
			claim_token TEXT,
			claimed_at  TIMESTAMPTZ,
			enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			priority    INTEGER NOT NULL DEFAULT 0,
			sequence    BIGSERIAL
		);
		CREATE INDEX %s_available_idx   ON %s (priority DESC, sequence) WHERE state = 'available';
		CREATE INDEX %s_in_progress_idx ON %s (claim_token) WHERE state = 'in_progress';
	`, table, table, table, table, table)
	if _, err := pool.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("create items table %q: %v", table, err)
	}
}

func seedSwapCollision(t *testing.T, pool *pgxpool.Pool, canonical string) {
	t.Helper()
	ctx := context.Background()
	depSchema := canonical + "_dep"
	stmts := []string{
		"CREATE SCHEMA " + canonical,
		"CREATE SCHEMA " + depSchema,
		"CREATE TABLE " + canonical + ".pinned (n INT)",
		"CREATE VIEW " + depSchema + ".dep AS SELECT n FROM " + canonical + ".pinned",
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("seed swap collision (%q): %v", s, err)
		}
	}
}

func requireEventKind(t *testing.T, ep harness.RimskyEndpoint, instanceID, kind, why string) {
	t.Helper()
	path := fmt.Sprintf("/v1/events?instance_id=%s&kind=%s", instanceID, kind)
	awaited.Until(t, fmt.Sprintf("event kind %q to land on the event log for instance %s — %s", kind, instanceID, why),
		func() bool {
			status, raw := ep.GetJSON(t, path, "")
			if status != http.StatusOK {
				return false
			}
			var resp struct {
				Events []struct {
					Kind string `json:"kind"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return false
			}
			for _, e := range resp.Events {
				if e.Kind == kind {
					return true
				}
			}
			return false
		})
}

func requireNoStagingAndDependentsIntact(t *testing.T, pool *pgxpool.Pool, canonical string) {
	t.Helper()
	ctx := context.Background()

	var staging int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.schemata
		   WHERE schema_name LIKE 'rimsky_stg_%'`,
	).Scan(&staging); err != nil {
		t.Fatalf("query residual staging schemas: %v", err)
	}
	if staging != 0 {
		t.Fatalf("a `rimsky_stg_` staging schema exists after an Open that must fail fast on the " +
			"external-dependent guard; no staging may be reserved on the reject path")
	}

	var pinnedExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables
		   WHERE table_schema = $1 AND table_name = 'pinned')`,
		canonical,
	).Scan(&pinnedExists); err != nil {
		t.Fatalf("query canonical pinned table: %v", err)
	}
	if !pinnedExists {
		t.Fatalf("canonical schema %q.pinned is gone; a rejected Open must never touch the canonical", canonical)
	}

	var depExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.views
		   WHERE table_schema = $1 AND table_name = 'dep')`,
		canonical+"_dep",
	).Scan(&depExists); err != nil {
		t.Fatalf("query external dependent view: %v", err)
	}
	if !depExists {
		t.Fatalf("external dependent view %q.dep was destroyed; the fail-fast guard must leave it intact "+
			"(the CASCADE data-loss regression this test guards has returned)", canonical+"_dep")
	}
}
