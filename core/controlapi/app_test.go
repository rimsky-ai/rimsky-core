// app_test.go — end-to-end HTTP tests for the control API. Each test
// spins up an httptest.Server wrapping the real chi router, backed by a
// throwaway Postgres container via pgtest.
//
// Coverage targets the post-stores-redesign-v2 template shape (stores
// with selector + intent + alias, locks-by-name, attributes,
// claim_resolutions, inherits) plus the renamed admin/claim routes.
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
	pgstore "github.com/fallguy/rimsky/core/store/postgres"
	"github.com/fallguy/rimsky/core/store/stub"
)

type harness struct {
	srv     *httptest.Server
	backend *pgstorage.PostgresStorageBackend
	pool    *pgxpool.Pool
	stores  *store.Registry
}

// newHarness boots Postgres, wires the app, and returns a harness +
// teardown. Builds a *store.Registry with one stub-filesystem store
// ("content") and one postgres store ("topics-ring") configured with a
// pick policy backed by an items table.
func newHarness(t *testing.T) (*harness, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	backend := pgstorage.New(pool)
	q := qpg.New(pool)

	itemsTable := "items_app_test_" + sanitizeForTable(uuid.NewString())
	createAppItemsTable(t, pool, itemsTable)

	reg := store.NewRegistry()
	reg.Register(pgstore.Factory{})
	reg.Register(stub.FilesystemFactory())
	_, err := reg.BuildAll(store.StoresConfig{
		Stores: map[string]map[string]any{
			"content": {
				"kind": stub.KindFilesystem,
			},
			"topics-ring": {
				"kind":            "postgres",
				"connection":      pool.Config().ConnString(),
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

	app := NewApp(AppDeps{
		Storage: backend,
		Queue:   q,
		Clock:   shared.SystemClock{},
		Logger:  shared.SilentLogger{},
		Stores:  reg,
		NamedLocks: store.NamedLocksConfig{
			Locks: map[string]store.NamedLockConfig{
				"topics-ring:concurrent": {Limit: 5},
			},
		},
	})
	srv := httptest.NewServer(app)

	h := &harness{srv: srv, backend: backend, pool: pool, stores: reg}
	return h, func() {
		srv.Close()
		teardown()
	}
}

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

// validTemplateBody builds a minimal valid template request matching the
// stores-redesign-v2 shape. Two executor-backed nodes; no stores or locks.
func validTemplateBody(name string) map[string]any {
	return map[string]any{
		"name":             name,
		"version":          "v1",
		"frame_resolution": "serial_queue",
		"nodes": []map[string]any{
			{"type": "root", "executor": "worker"},
			{"type": "child", "executor": "worker", "dependencies": []string{"root"}},
		},
	}
}

// templateWithStoresAndLocks returns a template body exercising the new
// stores-redesign-v2 fields: a node that takes a counting lock and holds
// a pick-policy claim against the postgres store, and a downstream node
// that reads from the filesystem store and resolves the held claim.
func templateWithStoresAndLocks(name string) map[string]any {
	return map[string]any{
		"name":             name,
		"version":          "v1",
		"frame_resolution": "serial_queue",
		"nodes": []map[string]any{
			{
				"type":     "claim-topic",
				"executor": "worker",
				"schedule": "* * * * *",
				"stores": []map[string]any{
					{"name": "topics-ring", "selector": "@queue", "intent": "rw"},
				},
				"locks": []map[string]any{
					{"name": "topics-ring:concurrent"},
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
				"claim_resolutions": map[string]any{
					"topics-ring": map[string]any{
						"on_commit":  "delete",
						"on_give_up": "release_to_head",
					},
				},
			},
			{
				"type":         "review",
				"executor":     "worker",
				"dependencies": []string{"claim-topic"},
				"stores": []map[string]any{
					{"name": "content", "selector": "items/x", "intent": "r"},
				},
				"inherits": []map[string]any{
					{"claim": "topics-ring"},
				},
			},
		},
	}
}

func TestTemplateLifecycle_DeployGetListDelete(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("alpha-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)

	status, out = h.httpJSON(t, "GET", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, tplID, out["id"])

	status, out = h.httpJSON(t, "GET", "/templates", nil)
	require.Equal(t, http.StatusOK, status, out)
	tpls, _ := out["templates"].([]any)
	require.GreaterOrEqual(t, len(tpls), 1)

	status, _ = h.httpJSON(t, "DELETE", "/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)

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
	require.NotEmpty(t, errs)
}

func TestTemplateDeploy_UnknownStore_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("unknown-store-" + uuid.NewString())
	nodes := body["nodes"].([]map[string]any)
	nodes[0]["stores"] = []map[string]any{
		{"name": "ghost-store", "selector": "x", "intent": "r"},
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

	status, out = h.httpJSON(t, "GET", "/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, ck, out["consumer_key"])

	status, out = h.httpJSON(t, "GET", "/instances/"+ck, nil)
	require.Equal(t, http.StatusOK, status, out)

	status, out = h.httpJSON(t, "GET", "/instances/"+instID+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)
	require.Len(t, nodes, 2)

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
	nodeRow := firstNode(t, h, inst)
	_, err := h.pool.Exec(ctx, `UPDATE rimsky_nodes SET state='fresh' WHERE id=$1`, nodeRow.ID)
	require.NoError(t, err)

	status, out := h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/invalidate", map[string]any{
		"reason": "manual-poke",
	})
	require.Equal(t, http.StatusOK, status, out)

	loaded, err := h.backend.Nodes().Get(ctx, nodeRow.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, loaded.State)

	var frameCount int
	require.NoError(t, h.pool.QueryRow(ctx, `
        SELECT COUNT(*) FROM rimsky_frames
        WHERE instance_id = $1 AND state = 'queued' AND $2 = ANY(source_node_ids)
    `, inst.ID, nodeRow.ID).Scan(&frameCount))
	require.GreaterOrEqual(t, frameCount, 1)
}

func TestOperatorReset_OnlyValidFromFailed(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "op-rst-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)

	status, _ := h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/reset", nil)
	require.Equal(t, http.StatusConflict, status)

	_, err := h.pool.Exec(ctx, `UPDATE rimsky_nodes SET state='failed' WHERE id=$1`, nodeRow.ID)
	require.NoError(t, err)
	status, _ = h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/reset", nil)
	require.Equal(t, http.StatusOK, status)

	loaded, err := h.backend.Nodes().Get(ctx, nodeRow.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFailed, loaded.State)
	require.Nil(t, loaded.FrameID)

	var sourceCount int
	require.NoError(t, h.pool.QueryRow(ctx, `
		SELECT count(*) FROM rimsky_frames
		WHERE instance_id = $1 AND state = 'queued' AND $2 = ANY(source_node_ids)
	`, inst.ID, nodeRow.ID).Scan(&sourceCount))
	require.GreaterOrEqual(t, sourceCount, 1)
}

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
	for _, k := range []string{"fresh", "stale", "running", "failed"} {
		_, present := counts[k]
		require.True(t, present, "missing count key %q", k)
	}
}

// TestClaimHoldersRoute_EmptyList verifies the new
// /lock-holders/{id}/claim-holders route is wired through NewApp.
func TestClaimHoldersRoute_EmptyList(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", "/lock-holders/"+uuid.NewString()+"/claim-holders", nil)
	require.Equal(t, http.StatusOK, status, out)
	holders, _ := out["holders"].([]any)
	require.Empty(t, holders)
}

// TestAdminPickPolicyInsert_RouteWired verifies the renamed admin route
// is wired through NewApp.
func TestAdminPickPolicyInsert_RouteWired(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := map[string]any{
		"items": []map[string]any{
			{"payload": map[string]any{"topic": "alpha"}},
			{"payload": map[string]any{"topic": "beta"}},
		},
	}
	status, out := h.httpJSON(t, "POST", "/admin/stores/topics-ring/pick-policies/@queue/items", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.Equal(t, float64(2), out["inserted"])

	status, _ = h.httpJSON(t, "POST", "/admin/stores/missing/pick-policies/@queue/items", body)
	require.Equal(t, http.StatusNotFound, status)
}

func TestAdminForceFire_RouteWired(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, _ := h.httpJSON(t, "POST", "/admin/scheduled-nodes/"+uuid.NewString()+"/force-fire", nil)
	require.Equal(t, http.StatusNoContent, status)
}

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

// createAppItemsTable creates the §12.12 schema for a postgres-store
// pick-policy items table.
func createAppItemsTable(t *testing.T, pool *pgxpool.Pool, name string) {
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

// sanitizeForTable swaps non-identifier characters for underscore.
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
