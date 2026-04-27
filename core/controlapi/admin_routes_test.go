// admin_routes_test.go — handler tests for the three new routes added by
// Task 33: GET /claims/{claim_id}/holders, POST /admin/claim-stores/{name}/items,
// and POST /admin/scheduled-nodes/{node_id}/force-fire.
//
// Tests build a minimal chi.Router with only the route under test and a real
// Postgres-backed StorageBackend (via pgtest), so they do not depend on the
// rest of the control-api wiring (templates / instances / nodes routes are
// covered separately in app_test.go).
package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

// adminHarness bundles a Postgres pool + storage backend for one test.
// Smaller than the full app harness in app_test.go because the new routes
// don't need queue, clock, or resource wiring.
type adminHarness struct {
	pool    *pgxpool.Pool
	backend *pgstorage.PostgresStorageBackend
}

func newAdminHarness(t *testing.T) (*adminHarness, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	backend := pgstorage.New(pool)
	return &adminHarness{pool: pool, backend: backend}, teardown
}

// buildRouter builds a chi.Router wrapping the supplied register call with
// the supplied AppDeps. Mirrors NewApp's middleware chain (just JSON
// content-type) so the handlers run in a realistic context.
func buildRouter(register func(chi.Router, AppDeps), deps AppDeps) http.Handler {
	r := chi.NewRouter()
	register(r, deps)
	return r
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

// ---- TestClaimsRoute ---------------------------------------------------

func TestClaimsRoute(t *testing.T) {
	t.Parallel()
	h, teardown := newAdminHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	deps := AppDeps{
		Storage: h.backend,
		Logger:  shared.SilentLogger{},
	}
	router := buildRouter(registerClaimsRoutes, deps)

	// Empty claim returns 200 with empty list.
	status, body := doJSON(t, router, http.MethodGet, "/claims/empty-claim/holders", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	var emptyResp struct {
		Holders []map[string]any `json:"holders"`
	}
	require.NoError(t, json.Unmarshal(body, &emptyResp))
	require.Empty(t, emptyResp.Holders)

	// Insert a claim-holder row directly via the storage layer, then fetch.
	// We need a real node row to satisfy the FK on holder_node_id; seed a
	// throwaway template + instance + node.
	holderNodeID := seedThrowawayNode(t, h)

	claimID := "claim-" + uuid.NewString()
	holderID := uuid.New()
	err := h.backend.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
		ID:           holderID,
		ClaimID:      claimID,
		StoreName:    "topics-ring",
		HolderNodeID: holderNodeID,
		OnCommit:     storage.ClaimHolderActionDelete,
		OnGiveUp:     storage.ClaimHolderActionReleaseToHead,
	}, nil)
	require.NoError(t, err)

	status, body = doJSON(t, router, http.MethodGet, "/claims/"+claimID+"/holders", nil)
	require.Equal(t, http.StatusOK, status, string(body))

	var resp struct {
		Holders []map[string]any `json:"holders"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Len(t, resp.Holders, 1)
	require.Equal(t, holderID.String(), resp.Holders[0]["id"])
	require.Equal(t, claimID, resp.Holders[0]["claim_id"])
	require.Equal(t, "topics-ring", resp.Holders[0]["store_name"])
	require.Equal(t, "delete", resp.Holders[0]["on_commit"])
	require.Equal(t, "release_to_head", resp.Holders[0]["on_give_up"])
	require.Equal(t, "active", resp.Holders[0]["state"])
}

// ---- TestAdminClaimStoresRoute -----------------------------------------

func TestAdminClaimStoresRoute(t *testing.T) {
	t.Parallel()
	h, teardown := newAdminHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	// Create the operator-owned items table per spec §9.10.
	itemsTable := "items_admin_route_test"
	createItemsTable(t, h.pool, itemsTable)

	// Build a *store.Registry with one claim-store entry pointing at that table.
	reg := store.NewRegistry()
	reg.Register(claimstorepg.Factory{Pool: h.pool})
	_, err := reg.BuildAll(store.StoresConfig{
		Stores: map[string]map[string]any{
			"inbound": {
				"kind":                       "claim_store",
				"backend":                    "postgres",
				"items_table":                itemsTable,
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_head",
				"visibility_timeout_seconds": 300,
			},
		},
	})
	require.NoError(t, err)

	deps := AppDeps{
		Storage: h.backend,
		Stores:  reg,
		Logger:  shared.SilentLogger{},
	}
	router := buildRouter(registerAdminClaimStoresRoutes, deps)

	// Happy path: insert two items.
	body := map[string]any{
		"items": []map[string]any{
			{"payload": map[string]any{"topic": "alpha"}},
			{"payload": map[string]any{"topic": "beta"}},
		},
	}
	status, raw := doJSON(t, router, http.MethodPost, "/admin/claim-stores/inbound/items", body)
	require.Equal(t, http.StatusCreated, status, string(raw))
	var resp struct {
		Inserted int `json:"inserted"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	require.Equal(t, 2, resp.Inserted)

	var n int
	require.NoError(t, h.pool.QueryRow(ctx,
		`SELECT count(*) FROM `+itemsTable+` WHERE state = 'available'`,
	).Scan(&n))
	require.Equal(t, 2, n)

	// Unknown store → 404.
	status, raw = doJSON(t, router, http.MethodPost, "/admin/claim-stores/missing/items", body)
	require.Equal(t, http.StatusNotFound, status, string(raw))

	// Empty items array → 400.
	status, raw = doJSON(t, router, http.MethodPost, "/admin/claim-stores/inbound/items",
		map[string]any{"items": []map[string]any{}})
	require.Equal(t, http.StatusBadRequest, status, string(raw))

	// Missing payload → 400.
	status, raw = doJSON(t, router, http.MethodPost, "/admin/claim-stores/inbound/items",
		map[string]any{"items": []map[string]any{{}}})
	require.Equal(t, http.StatusBadRequest, status, string(raw))

	// Nil registry → 503.
	depsNoReg := AppDeps{Storage: h.backend, Logger: shared.SilentLogger{}}
	router2 := buildRouter(registerAdminClaimStoresRoutes, depsNoReg)
	status, raw = doJSON(t, router2, http.MethodPost, "/admin/claim-stores/inbound/items", body)
	require.Equal(t, http.StatusServiceUnavailable, status, string(raw))
}

// ---- TestAdminForceFireRoute -------------------------------------------

func TestAdminForceFireRoute(t *testing.T) {
	t.Parallel()
	h, teardown := newAdminHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	deps := AppDeps{
		Storage: h.backend,
		Logger:  shared.SilentLogger{},
	}
	router := buildRouter(registerAdminScheduleRoutes, deps)

	// Seed a node + schedule row with next_fire_at well in the future.
	nodeID := seedThrowawayNode(t, h)
	future := time.Now().Add(24 * time.Hour)
	require.NoError(t, h.backend.Schedules().Register(ctx, storage.ScheduleRegisterInput{
		NodeID:     nodeID,
		CronExpr:   "*/5 * * * *",
		NextFireAt: future,
	}, nil))

	// Invalid UUID → 400.
	status, raw := doJSON(t, router, http.MethodPost, "/admin/scheduled-nodes/not-a-uuid/force-fire", nil)
	require.Equal(t, http.StatusBadRequest, status, string(raw))

	// Force-fire flips next_fire_at to ~now. We allow a small clock-skew
	// grace because `before` is the test-process clock while `next_fire_at`
	// comes from the postgres container's clock — the two are not
	// synchronized to better than a few ms.
	const skewGrace = 5 * time.Second
	before := time.Now().Add(-skewGrace)
	status, raw = doJSON(t, router, http.MethodPost, "/admin/scheduled-nodes/"+nodeID.String()+"/force-fire", nil)
	require.Equal(t, http.StatusNoContent, status, string(raw))

	rows, err := h.backend.Schedules().ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, nodeID, rows[0].NodeID)
	// next_fire_at should now be on or after `before-grace` (and far below `future`).
	require.False(t, rows[0].NextFireAt.Before(before),
		"next_fire_at %v should be >= before %v", rows[0].NextFireAt, before)
	require.True(t, rows[0].NextFireAt.Before(future.Add(-time.Hour)),
		"next_fire_at %v should be far below original future %v", rows[0].NextFireAt, future)

	// Force-fire on an unknown node is a no-op (still 204; the SQL UPDATE
	// matches zero rows).
	missing := uuid.New()
	status, raw = doJSON(t, router, http.MethodPost, "/admin/scheduled-nodes/"+missing.String()+"/force-fire", nil)
	require.Equal(t, http.StatusNoContent, status, string(raw))
}

// ---- helpers ------------------------------------------------------------

// seedThrowawayNode inserts a minimal template + instance + node and
// returns the node ID. Bypasses the broken handlers and uses raw SQL plus
// the storage layer directly so this test file is independent of the
// templates / instances / nodes routes.
func seedThrowawayNode(t *testing.T, h *adminHarness) shared.UUID {
	t.Helper()
	ctx := context.Background()

	tplID := uuid.New()
	instID := uuid.New()
	nodeID := uuid.New()

	// Templates and instances tables expect non-null spec/params; insert
	// minimal valid JSONB.
	_, err := h.pool.Exec(ctx,
		`INSERT INTO rimsky_templates (id, name, version, spec, deployed_at)
		 VALUES ($1, $2, 'v1', '{}'::jsonb, now())`,
		tplID, "tpl-"+uuid.NewString(),
	)
	require.NoError(t, err)

	_, err = h.pool.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_id, consumer_key, params, created_at)
		 VALUES ($1, $2, $3, '{}'::jsonb, now())`,
		instID, tplID, "ck-"+uuid.NewString(),
	)
	require.NoError(t, err)

	_, err = h.pool.Exec(ctx,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor, schedule_cron, state,
		   dependencies, current_error_class, retry_counter, action_index,
		   created_at, updated_at
		 ) VALUES (
		   $1, $2, 'root', 'worker', '', 'fresh',
		   ARRAY[]::uuid[], '', 0, 0, now(), now()
		 )`,
		nodeID, instID,
	)
	require.NoError(t, err)

	return nodeID
}

// createItemsTable creates the §9.10 schema for a claim-store items table.
func createItemsTable(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE TABLE `+name+` (
		item_id     UUID PRIMARY KEY,
		payload     JSONB NOT NULL,
		enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		state       TEXT NOT NULL DEFAULT 'available',
		claim_token UUID,
		claimed_at  TIMESTAMPTZ
	)`)
	require.NoError(t, err)
}
