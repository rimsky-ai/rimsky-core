// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package pgerrorclasses

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
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

	netName := harness.NewNetwork(ctx, t)
	substrate := harness.StartPostgresOnNetwork(ctx, t, netName, "store-pg")
	pool := dialSubstrate(ctx, t, substrate.HostDSN)

	createItemsTable(t, pool, queueItemsTable)

	storeEndpoint := harness.StartPostgresStore(ctx, t, netName, "store-postgres", harness.PostgresStoreSpec{
		Connection:     substrate.InternalDSN,
		WriteSemantics: "sync",
		PickPolicies: map[string]harness.PostgresStorePickPolicy{
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
		harness.WithClaimProducer("queue-store", storeEndpoint, "sync"),
		harness.WithExecutor("stub", execEndpoint),
	)

	templateID := deployTemplate(t, ep, map[string]any{
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

	instanceID := createInstance(t, ep, templateID, "ck-pg-claim-unavailable")

	requireEventKind(t, ep, instanceID,
		"terminal/error/pg/claim_unavailable", 90*time.Second,
		"the empty pick-policy items table must deliver the producer-declared "+
			"pg/claim_unavailable class as a real signal, not only the synthetic "+
			"acquire/unavailable")
}

func testNotReplaceableDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	substrate := harness.StartPostgresOnNetwork(ctx, t, netName, "store-pg")
	pool := dialSubstrate(ctx, t, substrate.HostDSN)

	seedSwapCollision(t, pool, canonicalSchema)

	storeEndpoint := harness.StartPostgresStore(ctx, t, netName, "store-postgres", harness.PostgresStoreSpec{
		Connection:     substrate.InternalDSN,
		WriteSemantics: "staged_async",
		EnableExecutor: true,
	})

	execEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("staged-store", storeEndpoint, "staged_async"),
		harness.WithExecutor("stub", execEndpoint),
	)

	templateID := deployTemplate(t, ep, map[string]any{
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

	instanceID := createInstance(t, ep, templateID, "ck-pg-not-replaceable")

	requireEventKind(t, ep, instanceID,
		"terminal/error/pg/not_atomically_replaceable", 90*time.Second,
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

func deployTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t, "/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func createInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "pg-error-classes", instanceKey)
	return resp.InstanceID
}

func requireEventKind(t *testing.T, ep harness.RimskyEndpoint, instanceID, kind string, deadline time.Duration, why string) {
	t.Helper()
	end := time.Now().Add(deadline)
	path := fmt.Sprintf("/v1/events?instance_id=%s&kind=%s", instanceID, kind)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, path, "")
		if status == http.StatusOK {
			var resp struct {
				Events []struct {
					Kind string `json:"kind"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, e := range resp.Events {
					if e.Kind == kind {
						return
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("event kind %q never landed on the event log for instance %s within %v — %s",
		kind, instanceID, deadline, why)
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
