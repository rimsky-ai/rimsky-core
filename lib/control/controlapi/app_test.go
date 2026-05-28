// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// app_test.go — end-to-end HTTP tests for the control API. Each test
// spins up an httptest.Server wrapping the real chi router, backed by
// a throwaway Postgres container via pgtest.OpenDriver.
//
// Coverage targets the stores redesign template shape (stores with
// selector + intent + alias, locks-by-name, attributes, inherits) plus
// the renamed admin/claim routes.
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
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

type harness struct {
	srv     *httptest.Server
	driver  persistence.Database
	persist persistence.Tables
	stores  *locks.Registry
	// logger is a CapturingLogger so tests can assert presence/absence
	// of structured log records (e.g. the
	// `instance.attribute_overrides_*` audit lines emitted on the
	// instance-create path). Tests that don't care about log records
	// can ignore this field.
	logger *shared.CapturingLogger
}

// newHarness boots Postgres, wires the app, and returns a harness +
// teardown. Builds a *locks.Registry with two unit-test fakes
// ("content" and "topics-ring") satisfying store.Store; the wire is
// not exercised here (the unit tests target template validation, route
// wiring, and persistence-backed paths — not the store protocol).
func newHarness(t *testing.T) (*harness, func()) {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()
	contentFake := storetest.NewFake("content", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	topicsFake := storetest.NewFake("topics-ring", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("content", contentFake)
	reg.Add("topics-ring", topicsFake)

	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("content", contentFake)
	lcReg.Add("topics-ring", topicsFake)

	capLog := shared.NewCapturingLogger()
	app := NewApp(AppDeps{
		Persist:       d.Tables(),
		Queue:         d.Queue(),
		Clock:         shared.SystemClock{},
		Logger:        capLog,
		Stores:        reg,
		LifecycleSubs: lcReg,
		NamedLocks: locks.NamedLocksConfig{
			Locks: map[string]locks.NamedLockConfig{
				"topics-ring:concurrent": {Limit: 5},
			},
		},
		// Wire the executor names referenced by validTemplateBody and
		// templateWithStoresAndLocks so the validator's
		// ExecutorDeclared hook actually runs (otherwise the hook is
		// silently nil and missing-executor templates pass deploy).
		// `unused-exec` is declared but intentionally not referenced by
		// any test template — used by
		// TestInstanceCreate_AttributeOverrides_RejectsExecutorNotReferencedByTemplate
		// to drive the validator's "declared-but-unused executor"
		// rejection branch end-to-end.
		Executors: map[string]ExecutorEntry{
			"worker":      {Transport: "grpc", Endpoint: "localhost:0"},
			"unused-exec": {Transport: "grpc", Endpoint: "localhost:0"},
		},
	})
	srv := httptest.NewServer(app)

	h := &harness{srv: srv, driver: d, persist: d.Tables(), stores: reg, logger: capLog}
	return h, func() {
		srv.Close()
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

// httpResponse pairs the parsed body + status for tests that need
// header-driven dispatch (e.g. Idempotency-Key).
type httpResponse struct {
	status int
	body   map[string]any
}

// httpJSONWithHeaders is httpJSON plus extra request headers. Used by
// the idempotency-key tests so the universal dedup path actually
// fires.
func (h *harness) httpJSONWithHeaders(t *testing.T, method, path string, body any, headers map[string]string) httpResponse {
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
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return httpResponse{status: resp.StatusCode, body: out}
}

// validTemplateBody builds a minimal valid template request matching
// the wrapped POST /templates body shape (`{spec: {...}}`). Two
// executor-backed nodes; no stores or locks.
func validTemplateBody(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
				{"type": "child", "executor": "worker", "subscribes": []map[string]any{{"node": "root", "type": "terminal/*"}}},
			},
		},
	}
}

// specOf returns the inner `spec` map of a wrapped POST /templates body.
// Lets tests that need to mutate the spec keep the wrapped body shape.
func specOf(body map[string]any) map[string]any {
	return body["spec"].(map[string]any)
}

// templateWithStoresAndLocks returns a wrapped template body exercising
// the new stores redesign fields: a node that takes a counting lock
// and holds a pick-policy claim against the postgres store, and a
// downstream node that reads from the filesystem store and resolves
// the held claim.
func templateWithStoresAndLocks(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{
					"type":     "claim-topic",
					"executor": "worker",
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
				},
				{
					"type":       "review",
					"executor":   "worker",
					"subscribes": []map[string]any{{"node": "claim-topic", "type": "terminal/*"}},
					"stores": []map[string]any{
						{"name": "content", "selector": "items/x", "intent": "r"},
					},
					"inherits": []map[string]any{
						{"claim": "topics-ring"},
					},
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
	delete(specOf(body), "name")
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
	spec := specOf(body)
	nodes := spec["nodes"].([]map[string]any)
	nodes[0]["stores"] = []map[string]any{
		{"name": "ghost-store", "selector": "x", "intent": "r"},
	}
	spec["nodes"] = nodes
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
}

// TestTemplateDeploy_ClaimResolutions_Rejected asserts that a template
// JSON body carrying a `claim_resolutions:` block (the v3 pre-cleanup
// shape, now removed per the 2026-04-30 stores cleanup) fails deploy
// with an unknown-field error rather than being silently accepted with
// the field dropped on the floor. The handler's JSON decoder uses
// DisallowUnknownFields() to surface this.
func TestTemplateDeploy_ClaimResolutions_Rejected(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("cr-rejected-" + uuid.NewString())
	spec := specOf(body)
	nodes := spec["nodes"].([]map[string]any)
	nodes[0]["claim_resolutions"] = map[string]any{
		"topics-ring": map[string]any{
			"on_commit":  "commit",
			"on_give_up": "abandon",
		},
	}
	spec["nodes"] = nodes
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "claim_resolutions")
}

func TestTemplateDeploy_DependencyCycle_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := map[string]any{
		"spec": map[string]any{
			"name":                  "cycle-" + uuid.NewString(),
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
			// Post-2026-05-14: the legacy `dependencies:` field retired;
			// the JSON decoder (`DisallowUnknownFields`) rejects bodies
			// carrying it. Subscription cycles between two nodes are no
			// longer rejected at deploy time — the wait-set semantics
			// turn them into a defer-loop across frames — so this test
			// pins the parse-time rejection of the retired field.
			"nodes": []map[string]any{
				{"type": "a", "executor": "worker", "dependencies": []string{"b"}},
				{"type": "b", "executor": "worker", "dependencies": []string{"a"}},
			},
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
	// Transition register → deployed; instance creation requires
	// state='deployed' per spec §2.2.
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	ck := "ck-" + uuid.NewString()
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
		"params":       map[string]any{"region": "us-east"},
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)
	require.Equal(t, tplID, out["template_hash"])
	require.Equal(t, ck, out["instance_key"])
	require.Equal(t, float64(2), out["node_count"])

	status, out = h.httpJSON(t, "GET", "/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, ck, out["instance_key"])

	status, out = h.httpJSON(t, "GET", "/instances/"+ck, nil)
	require.Equal(t, http.StatusOK, status, out)

	status, out = h.httpJSON(t, "GET", "/instances/"+instID+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)
	require.Len(t, nodes, 2)

	// Drive the instance terminal — DELETE refuses to fire the
	// OnInstanceTerminated fan-out against a still-active instance
	// (spec §2.4).
	pgtest.ExecForTest(context.Background(), t, h.driver,
		`UPDATE rimsky_instances SET terminated_at = now() WHERE id = $1`, instID)

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
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)

	var frameCount int
	pgtest.QueryRowForTest(ctx, t, h.driver,
		`SELECT count(*) FROM rimsky_frames f
		 JOIN rimsky_nodes n ON n.id = ANY(f.source_node_ids)
		 WHERE n.node_type = 'root' AND f.state = 'queued'`,
		nil, &frameCount)
	require.Equal(t, 1, frameCount, "expected root node to have a queued frame")
}

func TestInstanceDuplicateConsumerKey_Idempotent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("dup-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	ck := "ck-" + uuid.NewString()
	status, firstOut := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
	})
	require.Equal(t, http.StatusCreated, status)
	firstID := firstOut["instance_id"].(string)
	require.NotEmpty(t, firstID)

	// Per spec §2.2 idempotent re-create: a second POST with the same
	// (template_hash, instance_key) returns the existing row at 200 OK.
	status, secondOut := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
	})
	require.Equal(t, http.StatusOK, status, secondOut)
	require.Equal(t, firstID, secondOut["instance_id"], "idempotent re-create must return the existing instance_id")
}

func TestOperatorInvalidate(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	ctx := context.Background()
	inst := seedInstance(t, h, "op-inv-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)
	// Post-stage-3: 'fresh' = no in-flight run row. Delete any.
	pgtest.ExecForTest(ctx, t, h.driver, `DELETE FROM rimsky_node_runs WHERE node_id=$1 AND phase IN ('pending','active','held','parked')`, nodeRow.ID)

	status, out := h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/invalidate", map[string]any{
		"reason": "manual-poke",
	})
	require.Equal(t, http.StatusOK, status, out)

	var loaded *persistence.NodeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Nodes().Get(ctx, nodeRow.ID, tx)
		loaded = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, loaded.State)

	var frameCount int
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT COUNT(*) FROM rimsky_frames
        WHERE instance_id = $1 AND state = 'queued' AND $2 = ANY(source_node_ids)
    `, []any{inst.ID, nodeRow.ID}, &frameCount)
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

	// Post-stage-3: state lives on rimsky_node_runs. Seed a failed
	// terminal row to put the node in the failed state.
	pgtest.ExecForTest(ctx, t, h.driver, `DELETE FROM rimsky_node_runs WHERE node_id=$1`, nodeRow.ID)
	// We need a frame_id for the run row's NOT NULL constraint. Find
	// any frame for this instance, or insert one.
	var frameID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 ORDER BY queued_at DESC LIMIT 1
    `, []any{inst.ID}, &frameID)
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1
    `, []any{inst.ID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, active_terminal_at, run_scope_id)
        VALUES (gen_random_uuid(), $1, 'stub', ARRAY[]::text[], now(), 'failed', 'failed', $2, now(), $3)
    `, nodeRow.ID, frameID, mainScopeID)
	status, _ = h.httpJSON(t, "POST", "/nodes/"+nodeRow.ID.String()+"/reset", nil)
	require.Equal(t, http.StatusOK, status)

	var loaded *persistence.NodeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Nodes().Get(ctx, nodeRow.ID, tx)
		loaded = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFailed, loaded.State)
	require.Nil(t, loaded.FrameID)

	var sourceCount int
	pgtest.QueryRowForTest(ctx, t, h.driver, `
		SELECT count(*) FROM rimsky_frames
		WHERE instance_id = $1 AND state = 'queued' AND $2 = ANY(source_node_ids)
	`, []any{inst.ID, nodeRow.ID}, &sourceCount)
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
	pgtest.ExecForTest(ctx, t, h.driver, `DELETE FROM rimsky_node_runs WHERE node_id=$1 AND phase IN ('pending','active','held','parked')`, nodeRow.ID)
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

// (TestAdminForceFire_RouteWired retired by the 2026-05-15 plan B10 /
// D7 / E16 schedule-retirement cascade. The /admin/scheduled-nodes/.../
// force-fire endpoint is gone.)

func seedInstance(t *testing.T, h *harness, tplName string) persistence.InstanceRow {
	t.Helper()
	ctx := context.Background()
	_, out := h.httpJSON(t, "POST", "/templates", validTemplateBody(tplName))
	tplID := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	ck := "ck-" + uuid.NewString()
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
	})
	require.Equal(t, http.StatusCreated, status, out)
	id, err := uuid.Parse(out["instance_id"].(string))
	require.NoError(t, err)
	var inst *persistence.InstanceRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Instances().Get(ctx, id, tx)
		inst = r
		return err
	}))
	require.NotNil(t, inst)
	return *inst
}

func firstNode(t *testing.T, h *harness, inst persistence.InstanceRow) persistence.NodeRow {
	t.Helper()
	ctx := context.Background()
	var nodes []persistence.NodeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Nodes().ListByInstance(ctx, inst.ID, tx)
		nodes = r
		return err
	}))
	require.Greater(t, len(nodes), 0)
	return nodes[0]
}
