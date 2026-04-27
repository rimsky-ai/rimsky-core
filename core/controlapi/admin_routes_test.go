// admin_routes_test.go — handler tests for the admin routes under the
// stores-redesign-v2 surface:
//
//   - GET  /lock-holders/{lock_holder_id}/claim-holders
//   - POST /admin/stores/{name}/pick-policies/{selector}/items
//   - POST /admin/scheduled-nodes/{node_id}/force-fire
//
// Each test wires only the route under test against a real Postgres-backed
// StorageBackend (via pgtest); the rest of the control-api surface is
// covered in app_test.go.
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
	pgstore "github.com/fallguy/rimsky/core/store/postgres"
)

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

// TestClaimHoldersRoute verifies GET /lock-holders/{id}/claim-holders.
func TestClaimHoldersRoute(t *testing.T) {
	t.Parallel()
	h, teardown := newAdminHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	deps := AppDeps{
		Storage: h.backend,
		Logger:  shared.SilentLogger{},
	}
	router := buildRouter(registerClaimsRoutes, deps)

	// Missing UUID → 400.
	status, _ := doJSON(t, router, http.MethodGet, "/lock-holders/not-a-uuid/claim-holders", nil)
	require.Equal(t, http.StatusBadRequest, status)

	// Unknown lock-holder UUID returns 200 + empty list.
	emptyID := uuid.New().String()
	status, body := doJSON(t, router, http.MethodGet, "/lock-holders/"+emptyID+"/claim-holders", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	var emptyResp struct {
		Holders []map[string]any `json:"holders"`
	}
	require.NoError(t, json.Unmarshal(body, &emptyResp))
	require.Empty(t, emptyResp.Holders)

	// Insert a lock-holder + claim-holder pair, then re-fetch.
	holderNodeID := seedThrowawayNode(t, h)
	lockHolderID := seedRegionLockHolder(ctx, t, h, holderNodeID)
	claimHolderID := uuid.New()
	require.NoError(t, h.backend.ClaimHolders().Insert(ctx, storage.ClaimHolderInsertInput{
		ID:           claimHolderID,
		LockHolderID: lockHolderID,
		HolderNodeID: holderNodeID,
	}, nil))

	status, body = doJSON(t, router, http.MethodGet, "/lock-holders/"+lockHolderID.String()+"/claim-holders", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	var resp struct {
		Holders []map[string]any `json:"holders"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Len(t, resp.Holders, 1)
	require.Equal(t, claimHolderID.String(), resp.Holders[0]["id"])
	require.Equal(t, lockHolderID.String(), resp.Holders[0]["lock_holder_id"])
	require.Equal(t, holderNodeID.String(), resp.Holders[0]["holder_node_id"])
	require.Equal(t, "active", resp.Holders[0]["state"])
}

// TestAdminPickPolicyInsertRoute verifies POST
// /admin/stores/{name}/pick-policies/{selector}/items.
func TestAdminPickPolicyInsertRoute(t *testing.T) {
	t.Parallel()
	h, teardown := newAdminHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	itemsTable := "items_admin_route_test"
	createItemsTable(t, h.pool, itemsTable)

	reg := store.NewRegistry()
	reg.Register(pgstore.Factory{})
	_, err := reg.BuildAll(store.StoresConfig{
		Stores: map[string]map[string]any{
			"inbound": {
				"kind":            "postgres",
				"connection":      h.pool.Config().ConnString(),
				"write_semantics": "direct",
				"pick_policies": map[string]any{
					"@queue": map[string]any{
						"type":                       "queue",
						"items_table":                itemsTable,
						"on_commit_default":          "delete",
						"on_give_up_default":         "release_to_head",
						"visibility_timeout_seconds": 300,
					},
				},
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

	body := map[string]any{
		"items": []map[string]any{
			{"payload": map[string]any{"topic": "alpha"}},
			{"payload": map[string]any{"topic": "beta"}},
		},
	}
	status, raw := doJSON(t, router, http.MethodPost, "/admin/stores/inbound/pick-policies/@queue/items", body)
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
	status, _ = doJSON(t, router, http.MethodPost, "/admin/stores/missing/pick-policies/@queue/items", body)
	require.Equal(t, http.StatusNotFound, status)

	// Unknown selector → 400.
	status, _ = doJSON(t, router, http.MethodPost, "/admin/stores/inbound/pick-policies/@unknown/items", body)
	require.Equal(t, http.StatusBadRequest, status)

	// Empty items → 400.
	status, _ = doJSON(t, router, http.MethodPost, "/admin/stores/inbound/pick-policies/@queue/items",
		map[string]any{"items": []map[string]any{}})
	require.Equal(t, http.StatusBadRequest, status)

	// Nil registry → 503.
	depsNoReg := AppDeps{Storage: h.backend, Logger: shared.SilentLogger{}}
	router2 := buildRouter(registerAdminClaimStoresRoutes, depsNoReg)
	status, _ = doJSON(t, router2, http.MethodPost, "/admin/stores/inbound/pick-policies/@queue/items", body)
	require.Equal(t, http.StatusServiceUnavailable, status)

	// Percent-encoded `@` (i.e. `%40queue`) must resolve to the same
	// route as the raw form. URL-builder libraries auto-encode `@`;
	// operator tooling that does this should keep working. Spec/doc
	// contract: both forms reach the handler with the leading `@`.
	status, _ = doJSON(t, router, http.MethodPost, "/admin/stores/inbound/pick-policies/%40queue/items", body)
	require.Equal(t, http.StatusCreated, status, "percent-encoded @ should resolve to the same selector")

	// Double-encoded `@` (i.e. `%2540queue`) decodes to the literal
	// string `%40queue`, which is NOT a configured selector — reject
	// with 400 (rather than silently inserting under a phantom name).
	// This is the documented operator footgun.
	status, _ = doJSON(t, router, http.MethodPost, "/admin/stores/inbound/pick-policies/%2540queue/items", body)
	require.Equal(t, http.StatusBadRequest, status, "double-encoded @ should be rejected as unknown selector")
}

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

	nodeID := seedThrowawayNode(t, h)
	future := time.Now().Add(24 * time.Hour)
	require.NoError(t, h.backend.Schedules().Register(ctx, storage.ScheduleRegisterInput{
		NodeID:     nodeID,
		CronExpr:   "*/5 * * * *",
		NextFireAt: future,
	}, nil))

	status, _ := doJSON(t, router, http.MethodPost, "/admin/scheduled-nodes/not-a-uuid/force-fire", nil)
	require.Equal(t, http.StatusBadRequest, status)

	const skewGrace = 5 * time.Second
	before := time.Now().Add(-skewGrace)
	status, _ = doJSON(t, router, http.MethodPost, "/admin/scheduled-nodes/"+nodeID.String()+"/force-fire", nil)
	require.Equal(t, http.StatusNoContent, status)

	rows, err := h.backend.Schedules().ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, nodeID, rows[0].NodeID)
	require.False(t, rows[0].NextFireAt.Before(before))
	require.True(t, rows[0].NextFireAt.Before(future.Add(-time.Hour)))

	missing := uuid.New()
	status, _ = doJSON(t, router, http.MethodPost, "/admin/scheduled-nodes/"+missing.String()+"/force-fire", nil)
	require.Equal(t, http.StatusNoContent, status)
}

// seedThrowawayNode inserts a minimal template + instance + node and
// returns the node ID. Bypasses validation/handlers and uses raw SQL so
// this file is independent of the broader controlapi route surface.
func seedThrowawayNode(t *testing.T, h *adminHarness) shared.UUID {
	t.Helper()
	ctx := context.Background()

	tplID := uuid.New()
	instID := uuid.New()
	nodeID := uuid.New()

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

// seedRegionLockHolder inserts a region-kind lock-holder row anchored to
// the given node and returns its ID. Used by claim-holders route tests
// to satisfy the FK on rimsky_claim_holders.lock_holder_id.
func seedRegionLockHolder(ctx context.Context, t *testing.T, h *adminHarness, nodeID shared.UUID) shared.UUID {
	t.Helper()
	storeName := "test-store"
	intent := "rw"
	id := uuid.New()
	require.NoError(t, h.backend.Transaction(ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.backend.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 id,
			LockKind:           storage.LockKindRegion,
			StoreName:          &storeName,
			RegionData:         []byte(`"r-1"`),
			Intent:             &intent,
			HolderSupervisorID: "scenario-supervisor",
			HolderNodeID:       nodeID,
			ExpiresAt:          time.Now().Add(5 * time.Minute),
		}, tx)
	}))
	return id
}

// createItemsTable creates the §12.12 schema for a postgres-store
// pick-policy items table.
func createItemsTable(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE TABLE `+name+` (
		item_id     TEXT PRIMARY KEY,
		payload     JSONB NOT NULL,
		state       TEXT NOT NULL DEFAULT 'available',
		claim_token TEXT,
		claimed_at  TIMESTAMPTZ,
		enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		priority    INTEGER NOT NULL DEFAULT 0,
		sequence    BIGSERIAL
	)`)
	require.NoError(t, err)
}
