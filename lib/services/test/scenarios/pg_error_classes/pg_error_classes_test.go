// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package pgerrorclasses is the full-stack proof for
// S-pgstore-claim-unavailable-swap-failed-emit: the postgres store's two
// declared error classes — `pg/claim_unavailable` (an empty pick-policy
// items table / an unopenable claim) and `pg/swap_failed` (an
// atomic-staging swap collision at Commit) — must fire as REAL signals an
// operator-declared `error_types:` entry routes to, observable on the
// canonical event log (`GET /events`) as `terminal/error/pg/<class>`.
//
// The platform's claim-terminal error routing (`concept:error-type`) keys
// an `error_types:` chain by the PRODUCER-DECLARED error class. The pg
// store declares `pg/claim_unavailable` and `pg/swap_failed` in its
// executor's declaredErrorClasses (so an operator's `error_types:` config
// validates), but today neither class is ever the class the routing keys
// on:
//
//   - An empty pick-policy items table surfaces ONLY as the producer's
//     bare `OpenResponse_Unavailable`. Rimsky's acquisition-failure
//     routing turns that into the SYNTHETIC class `acquire/unavailable`,
//     never the producer-declared `pg/claim_unavailable`. So an operator
//     who keys `error_types: { pg/claim_unavailable: give_up }` never sees
//     the chain match, and the canonical `terminal/error/pg/claim_unavailable`
//     signal never lands on the event log.
//
//   - A staged-write swap collision returns a `pg/swap_failed`-classed
//     error from the producer's Commit RPC at auto-terminal, but that
//     producer-verb error is not routed through the holder node's
//     `error_types:` chain — so `terminal/error/pg/swap_failed` never
//     lands on the event log either.
//
// RED CONTRACT (this pass — AUTHSTORES-19): both sub-cases assert the
// canonical `terminal/error/pg/<class>` signal lands on `GET /events`.
// Neither does today, so both sub-cases FAIL. A later pass (AUTHSTORES-20)
// threads the producer-declared class through the acquisition-failure
// chain and routes the swap-collision Commit error through the holder's
// `error_types:` chain, turning this test green.
//
// The pg store runs as a CONTAINER on the shared docker network against
// its OWN substrate postgres (a second container, distinct from rimsky's
// state DB), reached from rimsky by a stable in-network alias that is up
// BEFORE rimsky boots — rimsky eager-dials its declared producers for a
// Capabilities handshake at startup and exits non-zero if any is
// unreachable. The rimsky stack is a real all-in-one container on real
// Postgres (testcontainers); run `make core-images` + `make
// service-images` first.
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

// queueItemsTable is the empty items table the sub-case-1 pick policy
// reads. It must exist in the store's substrate BEFORE the store boots
// (the store verifies its configured items_table at startup) and stay
// empty so Open returns Unavailable.
const queueItemsTable = "items_unavailable"

// queueSelector is the pick-policy selector keyed in the store config and
// referenced by the acquirer node's `stores:` block.
const queueSelector = "@queue"

// canonicalSchema is the staged-write claim's canonical view (the claim
// selector) for sub-case 2. It is pre-created with a depended-upon object
// so the atomic swap's `DROP SCHEMA canonical` (RESTRICT) collides,
// forcing `pg/swap_failed` at Commit.
const canonicalSchema = "production_swap_collision"

// TestPGErrorClasses_Delivered is the AUTHSTORES-19 RED proof: the pg
// store's `pg/claim_unavailable` and `pg/swap_failed` declared classes
// must reach a subscriber (here: the canonical event log an operator
// `error_types:` chain routes to). Both sub-cases FAIL today.
func TestPGErrorClasses_Delivered(t *testing.T) {
	t.Parallel()
	t.Run("claim_unavailable", testClaimUnavailableDelivered)
	t.Run("swap_failed", testSwapFailedDelivered)
}

// testClaimUnavailableDelivered drives an acquire against an EMPTY
// pick-policy items table through a real rimsky stack. The acquirer node
// declares `error_types: { pg/claim_unavailable: give_up }`. The
// observable contract: the canonical `terminal/error/pg/claim_unavailable`
// signal lands on the event log.
//
// RED today: the empty items table surfaces as the producer's bare
// Unavailable, which rimsky routes under the SYNTHETIC class
// `acquire/unavailable` — never the producer-declared
// `pg/claim_unavailable`. So the operator's chain never matches and
// `terminal/error/pg/claim_unavailable` never lands. (The node still
// settles failed via the no-policy fail-fast default, but under the WRONG
// class — the assertion here is on the DECLARED-class signal, not on the
// node merely failing.)
func testClaimUnavailableDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: substrate postgres and the store must be up and reachable
	// before rimsky boots — rimsky fires a Capabilities handshake against
	// every declared claim producer at startup and exits non-zero on miss.
	netName := harness.NewNetwork(ctx, t)
	substrate := harness.StartPostgresOnNetwork(ctx, t, netName, "store-pg")
	pool := dialSubstrate(ctx, t, substrate.HostDSN)

	// @constraint: the items table must exist (the store verifies its
	// configured items_table at startup) yet stay EMPTY so Open returns
	// Unavailable — the value path this RED proof drives.
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

	execEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("queue-store", storeEndpoint, "sync"),
		harness.WithExecutor("stub", execEndpoint),
	)

	// @deliberate: the `subscriber` node uses an INSTANCE-SCOPED
	// `terminal/error/*` wildcard, not a node-scoped declared-vocabulary
	// subscribe. The producer-declared `pg/claim_unavailable` class is owned
	// by the store, not the worker's executor, so a node-scoped subscribe
	// would be range-checked against the wrong vocabulary at registration.
	// The wildcard surface is what makes the GREEN routing fire on the
	// producer-declared leaf. The assertion below reads `GET /events`
	// directly — same `terminal/error/pg/claim_unavailable` row the
	// subscriber reacts to.
	templateID := deployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":             "pg-claim-unavailable",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					"stores": []map[string]any{
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
							"instance":               true,
							"type":                   "terminal/error/*",
							"wake_on_change":         true,
							"force_upstream_refresh": false,
						},
					},
				},
			},
		},
	})

	instanceID := createInstance(t, ep, templateID, "ck-pg-claim-unavailable")

	// @constraint: the observable for AUTHSTORES-19 is the canonical
	// `terminal/error/pg/claim_unavailable` signal on the event log — the
	// PRODUCER-DECLARED class, not the synthetic `acquire/unavailable`
	// rimsky routes the empty-queue Unavailable under today. RED until the
	// declared class is threaded through the acquisition-failure chain.
	requireEventKind(t, ep, instanceID,
		"terminal/error/pg/claim_unavailable", 90*time.Second,
		"the empty pick-policy items table must deliver the producer-declared "+
			"pg/claim_unavailable class as a real signal, not only the synthetic "+
			"acquire/unavailable")
}

// testSwapFailedDelivered drives a held-claim staged-write subgraph whose
// atomic-staging swap COLLIDES at Commit, through a real rimsky stack. The
// holder node declares `error_types: { pg/swap_failed: give_up }`. The
// observable contract: the canonical `terminal/error/pg/swap_failed`
// signal lands on the event log.
//
// The collision is forced by pre-creating the canonical schema (the claim
// selector) with a cross-schema view depending on a table inside it, so
// the swap's `DROP SCHEMA canonical` (RESTRICT) cannot complete — exactly
// the populated/depended-upon-canonical collision the store names
// `pg/swap_failed` for.
//
// RED today: the producer's Commit `pg/swap_failed` error fires at
// auto-terminal but is not routed through the holder node's `error_types:`
// chain, so `terminal/error/pg/swap_failed` never lands on the event log.
func testSwapFailedDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	substrate := harness.StartPostgresOnNetwork(ctx, t, netName, "store-pg")
	pool := dialSubstrate(ctx, t, substrate.HostDSN)

	// @constraint: the canonical schema must be pre-created with a
	// depended-upon object before rimsky boots so the store's atomic swap
	// (`DROP SCHEMA canonical` RESTRICT) collides at Commit. A view in a
	// SEPARATE schema depending on a table inside the canonical blocks the
	// non-CASCADE drop — exactly the `pg/swap_failed` collision under test.
	seedSwapCollision(t, pool, canonicalSchema)

	// @deliberate: staged_async with EnableExecutor — the same binary serves
	// both ClaimProducer (Open/Commit with the staging swap) and the
	// SQL-verifier Executor the held subgraph's verifier dispatches against,
	// so the swap-collision value path runs end-to-end on one peer.
	storeEndpoint := harness.StartPostgresStore(ctx, t, netName, "store-postgres", harness.PostgresStoreSpec{
		Connection:     substrate.InternalDSN,
		WriteSemantics: "staged_async",
		EnableExecutor: true,
	})

	execEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("staged-store", storeEndpoint, "staged_async"),
		harness.WithExecutor("stub", execEndpoint),
	)

	// @deliberate: the held-subgraph shape exists so auto-terminal fires the
	// aggregate Commit that drives the store's atomic swap into the seeded
	// collision. The acquirer/verifier dispatch the stub only because they
	// must SUCCEED for auto-terminal to fire — the swap and its collision
	// are the value path under test. The `subscriber` uses an
	// instance-scoped `terminal/error/*` wildcard for the same reason as
	// the claim_unavailable case: the producer-declared `pg/swap_failed`
	// class is owned by the store, not the dispatching executors. The
	// assertion below reads `GET /events` directly.
	templateID := deployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":             "pg-swap-failed",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "acquirer",
					"executor": "stub",
					"stores": []map[string]any{
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
						{"node": "acquirer", "type": "terminal/*", "wake_on_change": true, "force_upstream_refresh": false},
					},
				},
				{
					"type":     "subscriber",
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"instance":               true,
							"type":                   "terminal/error/*",
							"wake_on_change":         true,
							"force_upstream_refresh": false,
						},
					},
				},
			},
		},
	})

	instanceID := createInstance(t, ep, templateID, "ck-pg-swap-failed")

	// @deliberate: right-reason guard #1 — poll the substrate for the
	// `rimsky_stg_` staging schema the staged claim's Open must reserve. Its
	// APPEARANCE during the run is race-free positive proof the
	// atomic-staging value path engaged; the swap can only collide if a
	// staging schema was reserved to swap FROM.
	requireStagingSchemaReserved(t, pool, 120*time.Second)

	// @deliberate: right-reason guard #2 — after the settle window, the
	// canonical's pinned table AND the reserved staging schema must both
	// survive. A successful swap would have dropped the canonical and
	// consumed the staging; their joint survival is proof the swap was
	// attempted and COLLIDED (rolled-back swap tx leaves staging intact),
	// so the missing event signal below is for the right reason — not
	// because the swap silently succeeded.
	requireSwapCollidedAndLeftStaging(t, pool, canonicalSchema, 30*time.Second)

	// @constraint: the observable for AUTHSTORES-19 is the canonical
	// `terminal/error/pg/swap_failed` signal on the event log — the
	// producer-declared class routed to a claim-terminal error signal. RED
	// until the holder's `error_types:` chain receives the producer-verb
	// Commit error instead of letting it bubble as a tx-level error from
	// the auto-terminal drain.
	requireEventKind(t, ep, instanceID,
		"terminal/error/pg/swap_failed", 90*time.Second,
		"the forced atomic-staging swap collision must deliver the "+
			"producer-declared pg/swap_failed class as a real signal")
}

// dialSubstrate opens a host-side pgx pool against the store's substrate
// DSN, closed on t.Cleanup. Direct SQL access against the store's own
// substrate is the scenario test's white-box opinion (not rimsky's), so
// the pool lives here in the test, not in the pgx-free harness package
// (the `pgx-isolation` depguard allows pgx under
// `lib/services/test/scenarios/**`).
func dialSubstrate(ctx context.Context, t *testing.T, hostDSN string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, hostDSN)
	if err != nil {
		t.Fatalf("dial substrate pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// createItemsTable creates the pg-store pick-policy items table (empty)
// in the substrate. Mirrors the schema the store's pick-policy SELECT
// expects (see stores/postgres/store action-vocab tests). No rows are
// inserted, so Open returns Unavailable.
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

// seedSwapCollision pre-creates the canonical schema with a depended-upon
// object so the store's atomic swap (`DROP SCHEMA canonical` RESTRICT)
// collides at Commit. A view in a separate schema depending on a table
// inside the canonical blocks the non-CASCADE drop — the
// populated/depended-upon-canonical collision the store classes
// `pg/swap_failed`.
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

// deployTemplate POSTs body to /templates then deploys it; returns the
// template id.
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

// createInstance POSTs a new instance and returns its instance_id.
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
	return resp.InstanceID
}

// requireEventKind polls the canonical event log (`GET /events`, filtered
// by instance + kind) until at least one event of the given kind appears,
// or the deadline elapses. The event log is the operator-visible surface a
// subscriber / `error_types:` chain routes to (`concept:signal` — the
// canonical `terminal/error/<class>` row lands on `rimsky_events`). Fails
// hard on timeout — never a skip.
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

// requireSwapCollidedAndLeftStaging waits a settle window for the
// auto-terminal Commit to attempt the atomic swap, then asserts the swap
// COLLIDED: the canonical schema's pinned table (the depended-upon object
// that blocks the swap's `DROP SCHEMA canonical` RESTRICT) is still
// present AND at least one reserved `rimsky_stg_` staging schema still
// exists. A successful swap would have dropped the canonical and consumed
// the staging; their joint survival across the window is proof the swap
// was attempted and collided (the rolled-back swap tx leaves both intact),
// the right-reason guard for this RED gate. Fails hard if either is gone
// (the swap silently succeeded — not the collision the test forces).
func requireSwapCollidedAndLeftStaging(t *testing.T, pool *pgxpool.Pool, canonical string, settle time.Duration) {
	t.Helper()
	// @deliberate: wait the full settle window before checking — the
	// auto-terminal Commit needs time to run the swap before the assertion
	// on its (failed) effect is meaningful.
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
	}
	var pinnedExists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables
		   WHERE table_schema = $1 AND table_name = 'pinned')`,
		canonical,
	).Scan(&pinnedExists); err != nil {
		t.Fatalf("query canonical pinned table: %v", err)
	}
	if !pinnedExists {
		t.Fatalf("canonical schema %q.pinned is gone — the atomic swap must have "+
			"completed, but this test forces a collision (the depended-upon canonical "+
			"cannot be dropped under RESTRICT); the swap-collision value path did not "+
			"run as intended", canonical)
	}
	var staging int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.schemata
		   WHERE schema_name LIKE 'rimsky_stg_%'`,
	).Scan(&staging); err != nil {
		t.Fatalf("query residual staging schemas: %v", err)
	}
	if staging == 0 {
		t.Fatalf("the reserved `rimsky_stg_` staging schema is gone after the swap window — "+
			"a collided swap rolls back and leaves staging intact, so its absence means the "+
			"swap either succeeded or never ran; the swap-collision value path did not run as "+
			"intended for canonical %q", canonical)
	}
}

// requireStagingSchemaReserved polls the substrate until at least one
// per-claim staging schema (the `rimsky_stg_` prefix the store reserves
// at Open) appears, or the deadline elapses. Race-free positive proof the
// atomic-staging value path engaged: the swap can only collide if Open
// first reserved a staging schema to swap FROM. Fails hard on timeout.
func requireStagingSchemaReserved(t *testing.T, pool *pgxpool.Pool, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM information_schema.schemata
			   WHERE schema_name LIKE 'rimsky_stg_%'`,
		).Scan(&n); err != nil {
			t.Fatalf("query staging schemas: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no `rimsky_stg_` staging schema ever appeared in the substrate within %v — "+
		"the staged claim Open did not reserve a staging schema, so the atomic-staging "+
		"swap-collision value path did not engage (check the store's write_semantics and "+
		"the selector's schema-identifier shape)", deadline)
}
