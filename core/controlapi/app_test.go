// app_test.go — end-to-end HTTP tests for the control API.
// Each Test* spins up an httptest.Server wrapping the real chi router,
// backed by a throwaway Postgres container via pgtest.
//
// Coverage targets the post-stores-redesign template shape (stores, locks,
// attributes, claim_resolutions) and validation pipeline (node.ValidateTemplate
// against an injected store-kind lookup), plus the new Task 33 routes
// (/claims/{id}/holders, /admin/claim-stores/{name}/items,
// /admin/scheduled-nodes/{id}/force-fire) wired through the full app router.
package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	qpg "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

// harness bundles a running HTTP server + storage backend for one test.
type harness struct {
	srv     *httptest.Server
	backend *pgstorage.PostgresStorageBackend
	pool    *pgxpool.Pool
	stores  *store.Registry
}

// newHarness boots Postgres, wires the app, and returns a harness + teardown.
// Builds a *store.Registry with one filesystem store ("content") and one
// claim store ("topics-ring") so templates referencing those names validate
// cleanly without needing per-test factory wiring.
func newHarness(t *testing.T) (*harness, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	backend := pgstorage.New(pool)
	q := qpg.New(pool)

	// Items table backing the claim store. Each test owns its own table to
	// stay independent under -parallel.
	itemsTable := "items_app_test_" + sanitizeForTable(uuid.NewString())
	createAppItemsTable(t, pool, itemsTable)

	reg := store.NewRegistry()
	reg.Register(claimstorepg.Factory{Pool: pool})
	reg.Register(stubFsFactory{})
	_, err := reg.BuildAll(store.StoresConfig{
		Stores: map[string]map[string]any{
			"content": {
				"kind": "filesystem",
			},
			"topics-ring": {
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

	app := NewApp(AppDeps{
		Storage: backend,
		Queue:   q,
		Clock:   shared.SystemClock{},
		Logger:  shared.SilentLogger{},
		Stores:  reg,
	})
	srv := httptest.NewServer(app)

	h := &harness{srv: srv, backend: backend, pool: pool, stores: reg}
	return h, func() {
		srv.Close()
		teardown()
	}
}

// httpJSON issues a request and decodes the JSON response.
func (h *harness) httpJSON(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, reqBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// validTemplateBody builds a small, valid template request matching the
// post-redesign shape. Two nodes (root → child), both executor-backed, no
// stores or locks. Tests mutate the returned map to exercise specific
// failure modes.
func validTemplateBody(name string) map[string]any {
	return map[string]any{
		"name":             name,
		"version":          "v1",
		"frame_resolution": "serial_queue",
		"nodes": []map[string]any{
			{
				"type":     "root",
				"executor": "worker",
			},
			{
				"type":         "child",
				"executor":     "worker",
				"dependencies": []string{"root"},
			},
		},
	}
}

// templateWithStoresAndLocks returns a template body exercising the new
// fields: a node that takes a counting lock and reads from the filesystem
// store, plus a downstream node that holds a claim from the claim store
// and resolves it on a leaf.
func templateWithStoresAndLocks(name string) map[string]any {
	return map[string]any{
		"name":             name,
		"version":          "v1",
		"frame_resolution": "serial_queue",
		"nodes": []map[string]any{
			{
				"type":     "claim-topic",
				"schedule": "* * * * *",
				"stores": []map[string]any{
					{"name": "topics-ring", "claim": true, "hold": true},
				},
				"locks": []map[string]any{
					{"name": "topics-ring:concurrent", "mode": "counting", "limit": 5},
				},
				"attributes": map[string]any{
					"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"area": map[string]any{
								"type":   "string",
								"source": "{{claim.topics-ring.payload.area}}",
							},
						},
						"required": []string{"area"},
					},
				},
			},
			{
				"type":         "review",
				"executor":     "worker",
				"dependencies": []string{"claim-topic"},
				"stores": []map[string]any{
					{"name": "content", "read": []string{"items/**"}},
				},
				"claim_resolutions": []map[string]any{
					{"source": "claim-topic", "store": "topics-ring"},
				},
			},
		},
	}
}

// ----------------- Tests ----------------------------------------------

func TestTemplateLifecycle_DeployGetListDelete(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("alpha-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)

	// Get
	status, out = h.httpJSON(t, "GET", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, tplID, out["id"])

	// List
	status, out = h.httpJSON(t, "GET", "/templates", nil)
	require.Equal(t, http.StatusOK, status, out)
	tpls, _ := out["templates"].([]any)
	require.GreaterOrEqual(t, len(tpls), 1)

	// Delete
	status, _ = h.httpJSON(t, "DELETE", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)

	// Get 404 after delete
	status, _ = h.httpJSON(t, "GET", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestTemplateDeploy_NewShape_StoresAndLocks(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := templateWithStoresAndLocks("stores-lc-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])
}

func TestTemplateDeploy_MissingName_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("bad")
	delete(body, "name")
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "template validation")
	errs, _ := out["validation_errors"].([]any)
	require.NotEmpty(t, errs, "expected validation_errors in body, got %+v", out)
}

func TestTemplateDeploy_UnknownStore_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("unknown-store-" + uuid.NewString())
	nodes := body["nodes"].([]map[string]any)
	nodes[0]["stores"] = []map[string]any{
		{"name": "ghost-store", "read": []string{"x/**"}},
	}
	body["nodes"] = nodes
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestTemplateDeploy_ClaimOnFilesystem_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("claim-on-fs-" + uuid.NewString())
	nodes := body["nodes"].([]map[string]any)
	nodes[0]["stores"] = []map[string]any{
		{"name": "content", "claim": true},
	}
	body["nodes"] = nodes
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestTemplateDeploy_DependencyCycle_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := map[string]any{
		"name":             "cycle-" + uuid.NewString(),
		"version":          "v1",
		"frame_resolution": "serial_queue",
		"nodes": []map[string]any{
			{"type": "a", "executor": "worker", "dependencies": []string{"b"}},
			{"type": "b", "executor": "worker", "dependencies": []string{"a"}},
		},
	}
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestTemplateDeploy_BadLockMode_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("bad-lock-" + uuid.NewString())
	nodes := body["nodes"].([]map[string]any)
	nodes[0]["locks"] = []map[string]any{
		{"name": "x", "mode": "ghost"},
	}
	body["nodes"] = nodes
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestInstanceLifecycle_CreateGetDelete(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-lc-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)

	ck := "ck-" + uuid.NewString()
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template_id":  tplID,
		"consumer_key": ck,
		"params":       map[string]any{"region": "us-east"},
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)
	require.Equal(t, float64(2), out["node_count"])

	// Get by id
	status, out = h.httpJSON(t, "GET", "/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, ck, out["consumer_key"])

	// Get by consumer_key
	status, out = h.httpJSON(t, "GET", "/instances/"+ck, nil)
	require.Equal(t, http.StatusOK, status, out)

	// Nodes listing
	status, out = h.httpJSON(t, "GET", "/instances/"+instID+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)
	require.Len(t, nodes, 2)

	// Delete
	status, _ = h.httpJSON(t, "DELETE", "/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "GET", "/instances/"+instID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestInstanceCreate_RootEnqueued(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("enq-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID := out["template_id"].(string)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template_id":  tplID,
		"consumer_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)

	// Under frame resolution, the instance factory enqueues a frame for
	// root executor nodes; the scheduler tick advances the frame and
	// enqueues the dispatch. Confirm a queued frame exists for the root
	// node (per docs/specs/2026-04-26-frame-resolution-design.md §3.1).
	var frameCount int
	require.NoError(t, h.pool.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_frames f
		 JOIN rimsky_nodes n ON n.id = ANY(f.source_node_ids)
		 WHERE n.node_type = 'root' AND f.state = 'queued'`,
	).Scan(&frameCount))
	require.Equal(t, 1, frameCount, "expected root node to have a queued frame")
}

func TestInstanceDuplicateConsumerKey_409(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("dup-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID := out["template_id"].(string)

	ck := "ck-" + uuid.NewString()
	status, _ := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template_id":  tplID,
		"consumer_key": ck,
	})
	require.Equal(t, http.StatusCreated, status)

	status, out = h.httpJSON(t, "POST", "/instances", map[string]any{
		"template_id":  tplID,
		"consumer_key": ck,
	})
	require.Equal(t, http.StatusConflict, status, out)
}

func TestOperatorInvalidate(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	ctx := context.Background()
	inst := seedInstance(t, h, "op-inv-"+uuid.NewString())
	// Grab a node, force its state to fresh so invalidate has something to do.
	nodeRow := firstNode(t, h, inst)
	_, err := h.pool.Exec(ctx, `UPDATE rimsky_nodes SET state='fresh' WHERE id=$1`, nodeRow.ID)
	require.NoError(t, err)

	status, out := h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/invalidate", map[string]any{
		"reason": "manual-poke",
	})
	require.Equal(t, http.StatusOK, status, out)

	// Under frame resolution, invalidate enqueues a rimsky_frames row; the
	// node remains in its prior state until the frame engine advances the
	// frame (per docs/specs/2026-04-26-frame-resolution-design.md §3.1).
	loaded, err := h.backend.Nodes().Get(ctx, nodeRow.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, loaded.State)

	var frameCount int
	require.NoError(t, h.pool.QueryRow(ctx, `
        SELECT COUNT(*) FROM rimsky_frames
        WHERE instance_id = $1 AND state = 'queued' AND $2 = ANY(source_node_ids)
    `, inst.ID, nodeRow.ID).Scan(&frameCount))
	// At least one — instance creation may have already enqueued a frame
	// for the root node; the operator invalidate adds another (serial_queue)
	// or coalesces (coalesce). We just assert presence.
	require.GreaterOrEqual(t, frameCount, 1,
		"invalidate should result in at least one queued frame for this node")
}

func TestOperatorReset_OnlyValidFromFailed(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "op-rst-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)

	// Reset on a stale node should 409.
	status, _ := h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/reset", nil)
	require.Equal(t, http.StatusConflict, status)

	// Force failed then reset should 200.
	_, err := h.pool.Exec(ctx, `UPDATE rimsky_nodes SET state='failed' WHERE id=$1`, nodeRow.ID)
	require.NoError(t, err)
	status, _ = h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/reset", nil)
	require.Equal(t, http.StatusOK, status)

	// Under the frame-resolution model (review Issue 2 / 16), reset
	// drives through frame.EnqueueOrCoalesce: the node remains 'failed'
	// in the DB until the scheduler tick advances the queued frame and
	// the frame engine writes 'stale' + new frame_id atomically. The
	// handler does clear the prior frame_id and enqueue a queued frame
	// — assert that contract instead of the post-tick state (the test
	// harness has no scheduler running).
	loaded, err := h.backend.Nodes().Get(ctx, nodeRow.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, loaded.State,
		"node remains failed until the frame engine advances the queued frame")
	require.Nil(t, loaded.FrameID,
		"reset must clear the prior frame_id pointing at the failed frame")

	// At least one queued frame should now reference this node as a source.
	var sourceCount int
	require.NoError(t, h.pool.QueryRow(ctx, `
		SELECT count(*) FROM rimsky_frames
		WHERE instance_id = $1 AND state = 'queued' AND $2 = ANY(source_node_ids)
	`, inst.ID, nodeRow.ID).Scan(&sourceCount))
	require.GreaterOrEqual(t, sourceCount, 1,
		"reset must enqueue (or coalesce into) a queued frame whose sources include the reset node")
}

// TestOperatorKill_RouteRemoved confirms POST /nodes/{id}/kill returns 404.
// The kill semantic is replaced by frame-resolution invalidate semantics
// (per docs/specs/2026-04-26-frame-resolution-design.md §5.4).
func TestOperatorKill_RouteRemoved(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "op-kill-removed-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)

	status, _ := h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/kill", nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestEventsList(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	// Seed an event by kicking an invalidate through.
	inst := seedInstance(t, h, "ev-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)
	_, err := h.pool.Exec(ctx, `UPDATE rimsky_nodes SET state='fresh' WHERE id=$1`, nodeRow.ID)
	require.NoError(t, err)
	status, _ := h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/invalidate", map[string]any{
		"reason": "ev-test",
	})
	require.Equal(t, http.StatusOK, status)

	status, out := h.httpJSON(t, "GET", "/events?node_id="+nodeRow.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	events, _ := out["events"].([]any)
	require.Greater(t, len(events), 0)
}

func TestHealth(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", "/health", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "ok", out["status"])
	counts, ok := out["node_counts"].(map[string]any)
	require.True(t, ok)
	// All four keys present even with zero counts.
	for _, k := range []string{"fresh", "stale", "running", "failed"} {
		_, present := counts[k]
		require.True(t, present, "missing count key %q", k)
	}
}

// --------- Task 33 routes wired through the full app router ---------

func TestClaimsRoute_EmptyHolders(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", "/claims/empty/holders", nil)
	require.Equal(t, http.StatusOK, status, out)
	holders, _ := out["holders"].([]any)
	require.Empty(t, holders)
}

func TestAdminClaimStores_RouteWired(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	// Insert two items into the registered topics-ring claim store.
	body := map[string]any{
		"items": []map[string]any{
			{"payload": map[string]any{"topic": "alpha"}},
			{"payload": map[string]any{"topic": "beta"}},
		},
	}
	status, out := h.httpJSON(t, "POST", "/admin/claim-stores/topics-ring/items", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.Equal(t, float64(2), out["inserted"])

	// Unknown store → 404.
	status, _ = h.httpJSON(t, "POST", "/admin/claim-stores/missing/items", body)
	require.Equal(t, http.StatusNotFound, status)
}

func TestAdminForceFire_RouteWired(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	// Force-fire on a missing node is a 204 no-op (the SQL UPDATE matches
	// zero rows) — sufficient to confirm the route is wired through NewApp.
	status, _ := h.httpJSON(t, "POST", "/admin/scheduled-nodes/"+uuid.NewString()+"/force-fire", nil)
	require.Equal(t, http.StatusNoContent, status)
}

// ----------------- helpers ---------------------------------------------

// seedInstance deploys a fresh template and creates an instance returning the
// instance row as read from the DB.
func seedInstance(t *testing.T, h *harness, tplName string) storage.InstanceRow {
	t.Helper()
	ctx := context.Background()
	_, out := h.httpJSON(t, "POST", "/templates", validTemplateBody(tplName))
	tplID := out["template_id"].(string)
	ck := "ck-" + uuid.NewString()
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template_id":  tplID,
		"consumer_key": ck,
	})
	require.Equal(t, http.StatusCreated, status, out)
	id, err := uuid.Parse(out["instance_id"].(string))
	require.NoError(t, err)
	inst, err := h.backend.Instances().Get(ctx, id, nil)
	require.NoError(t, err)
	require.NotNil(t, inst)
	return *inst
}

func firstNode(t *testing.T, h *harness, inst storage.InstanceRow) storage.NodeRow {
	t.Helper()
	ctx := context.Background()
	nodes, err := h.backend.Nodes().ListByInstance(ctx, inst.ID, nil)
	require.NoError(t, err)
	require.Greater(t, len(nodes), 0)
	return nodes[0]
}

// createAppItemsTable creates the §9.10 schema for a claim-store items
// table. Mirrors the helper in admin_routes_test.go but kept inline so this
// file is self-contained.
func createAppItemsTable(t *testing.T, pool *pgxpool.Pool, name string) {
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

// sanitizeForTable swaps characters that are illegal in a SQL identifier
// for an underscore. Used only to derive a unique items-table name from a
// UUID; collisions don't matter because the table is per-test.
func sanitizeForTable(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// stubFsFactory is a minimal filesystem-kind store factory used by tests
// that need a "filesystem" entry in the registry without depending on the
// real filesystem store package (which has its own integration concerns).
// All Store methods are no-ops; the validator only consults Kind() / Name(),
// not behaviour, so no-op stubs are sufficient.
type stubFsFactory struct{}

func (stubFsFactory) Kind() string { return "filesystem" }
func (stubFsFactory) Build(name string, _ map[string]any) (store.Store, error) {
	return &stubFsStore{name: name}, nil
}

type stubFsStore struct{ name string }

func (s *stubFsStore) Kind() string                     { return "filesystem" }
func (s *stubFsStore) Name() string                     { return s.name }
func (s *stubFsStore) Capabilities() store.Capabilities { return store.Capabilities{} }
func (s *stubFsStore) LockEligible(context.Context, store.LockSpec) (bool, error) {
	return true, nil
}
func (s *stubFsStore) RegionsConflict(_, _ any) bool         { return false }
func (s *stubFsStore) UnmarshalRegion(_ []byte) (any, error) { return nil, nil }
func (s *stubFsStore) AcquireLock(context.Context, store.LockSpec) (store.LockHandle, store.ClaimResult, error) {
	return store.LockHandle{}, store.ClaimResult{}, nil
}
func (s *stubFsStore) OpenHandle(context.Context, store.LockHandle, bool) (store.NativeHandle, error) {
	return nil, nil
}
func (s *stubFsStore) Commit(context.Context, store.LockHandle) (store.CommitResult, error) {
	return store.CommitResult{}, nil
}
func (s *stubFsStore) ReleaseLock(context.Context, store.LockHandle, store.ReleaseAction) error {
	return nil
}
