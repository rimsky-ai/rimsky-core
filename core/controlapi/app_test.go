// app_test.go — end-to-end HTTP tests for the control API.
// Each Test* spins up an httptest.Server wrapping the real chi router,
// backed by a throwaway Postgres container via pgtest.
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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/node"
	qpg "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/resource/inlinejsonb"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// harness bundles a running HTTP server + storage backend for one test.
type harness struct {
	srv     *httptest.Server
	backend *pgstorage.PostgresStorageBackend
	pool    *pgxpool.Pool
}

// newHarness boots Postgres, wires the app, and returns a harness + teardown.
// Registers the inline-jsonb factory (idempotent) so templates referencing it
// validate and provision cleanly.
func newHarness(t *testing.T) (*harness, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	backend := pgstorage.New(pool)
	q := qpg.New(pool)

	// Per-test factory registry — avoids aliasing across parallel tests.
	reg := resource.NewRegistry()
	reg.Register("inline-jsonb", inlinejsonb.Factory{
		StorageRegistry: backend.Resources(),
	})

	app := NewApp(AppDeps{
		Storage:           backend,
		Queue:             q,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		ResourceFactories: reg,
	})
	srv := httptest.NewServer(app)

	h := &harness{srv: srv, backend: backend, pool: pool}
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

// validTemplateBody builds a small, valid template request. Tests mutate the
// returned map to exercise specific failure modes.
func validTemplateBody(name string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": "v1",
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

func TestTemplateDeploy_InvalidRejected400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	// Missing name.
	body := validTemplateBody("bad")
	delete(body, "name")
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "template validation")

	// Bad placeholder.
	body = validTemplateBody("bad-placeholder-" + uuid.NewString())
	nodes := body["nodes"].([]map[string]any)
	nodes[0]["concurrency_tags"] = []string{"{not_a_placeholder}"}
	body["nodes"] = nodes
	status, out = h.httpJSON(t, "POST", "/templates", body)
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

	// Reload; should be stale now.
	loaded, err := h.backend.Nodes().Get(ctx, nodeRow.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, loaded.State)
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

	loaded, err := h.backend.Nodes().Get(ctx, nodeRow.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, loaded.State)
}

func TestOperatorKill_SetsKillRequested(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "op-kill-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)

	status, _ := h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/kill", nil)
	require.Equal(t, http.StatusOK, status)

	loaded, err := h.backend.Nodes().Get(ctx, nodeRow.ID, nil)
	require.NoError(t, err)
	require.True(t, loaded.KillRequested)
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

// silence unused-imports if node/time become used only conditionally.
var _ = node.TemplateSpec{}
var _ = time.Now
