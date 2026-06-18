// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
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
	logger  *shared.CapturingLogger
}

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

type httpResponse struct {
	status int
	body   map[string]any
}

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

func validTemplateBody(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
			"messages": []map[string]any{
				{"type": "system/invalidate"},
			},
			"nodes": []map[string]any{
				{"type": "root", "executor": "worker"},
				{"type": "child", "executor": "worker", "subscribes": []map[string]any{{"node": "root", "type": "terminal/*", "wake_on_change": true, "force_upstream_refresh": false}}},
			},
		},
	}
}

func specOf(body map[string]any) map[string]any {
	return body["spec"].(map[string]any)
}

func templateWithStoresAndLocks(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
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
					"subscribes": []map[string]any{{"node": "claim-topic", "type": "terminal/*", "wake_on_change": true, "force_upstream_refresh": false}},
					"stores": []map[string]any{
						{"name": "content", "selector": "items/x", "intent": "r"},
					},
					"holds": map[string]any{
						"topics-ring": map[string]any{"from": "claim-topic"},
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
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)

	status, out = h.httpJSON(t, "GET", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, tplID, out["id"])

	status, out = h.httpJSON(t, "GET", "/v1/templates", nil)
	require.Equal(t, http.StatusOK, status, out)
	tpls, _ := out["templates"].([]any)
	require.GreaterOrEqual(t, len(tpls), 1)

	status, _ = h.httpJSON(t, "DELETE", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "GET", "/v1/templates/"+tplID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestTemplateDeploy_NewShape_StoresAndLocks(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := templateWithStoresAndLocks("stores-lc-" + uuid.NewString())
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	require.NotEmpty(t, out["template_id"])
}

func TestTemplateDeploy_MissingName_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := validTemplateBody("bad")
	delete(specOf(body), "name")
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
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
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
}

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
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "claim_resolutions")
}

func TestTemplateDeploy_DependencyCycle_400(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := map[string]any{
		"spec": map[string]any{
			"name":    "cycle-" + uuid.NewString(),
			"version": "v1",
			"nodes": []map[string]any{
				{"type": "a", "executor": "worker", "dependencies": []string{"b"}},
				{"type": "b", "executor": "worker", "dependencies": []string{"a"}},
			},
		},
	}
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestInstanceLifecycle_CreateGetDelete(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("inst-lc-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	ck := "ck-" + uuid.NewString()
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
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

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, ck, out["instance_key"])

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+ck, nil)
	require.Equal(t, http.StatusOK, status, out)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)
	require.Len(t, nodes, 2)

	pgtest.ExecForTest(context.Background(), t, h.driver,
		`UPDATE rimsky_instances SET terminated_at = now() WHERE id = $1`, instID)

	status, _ = h.httpJSON(t, "DELETE", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestInstanceCreate_IsIdle(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("idle-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	// @story: instance-create-is-idle
	var frameCount int
	pgtest.QueryRowForTest(ctx, t, h.driver,
		`SELECT count(*) FROM rimsky_frames WHERE instance_id = $1`,
		[]any{instID}, &frameCount)
	require.Equal(t, 0, frameCount,
		"post-create the instance must have zero frames until a sender posts a message")

	var msgCount int
	pgtest.QueryRowForTest(ctx, t, h.driver,
		`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1`,
		[]any{instID}, &msgCount)
	require.Equal(t, 0, msgCount,
		"post-create the instance's message ledger must be empty (no synthetic envelope)")
}

func TestInstanceDuplicateConsumerKey_Idempotent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("dup-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	ck := "ck-" + uuid.NewString()
	status, firstOut := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
	})
	require.Equal(t, http.StatusCreated, status)
	firstID := firstOut["instance_id"].(string)
	require.NotEmpty(t, firstID)

	status, secondOut := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": ck,
	})
	require.Equal(t, http.StatusOK, status, secondOut)
	require.Equal(t, firstID, secondOut["instance_id"], "idempotent re-create must return the existing instance_id")
}

func TestOperatorReset_OnlyValidFromFailed(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "op-rst-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)

	status, _ := h.httpJSON(t, "POST", "/v1/nodes/"+nodeRow.ID.String()+"/reset", nil)
	require.Equal(t, http.StatusConflict, status)

	pgtest.ExecForTest(ctx, t, h.driver, `DELETE FROM rimsky_node_runs WHERE node_id=$1`, nodeRow.ID)
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver, `
        SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1
    `, []any{inst.ID}, &mainScopeID)
	msgID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_messages
            (id, instance_id, type, sender_kind, sender, payload, received_at)
        VALUES ($1, $2, '', 'operator', 'test', E'{}'::bytea, now())
    `, msgID, inst.ID)
	frameID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, state, queued_at, started_at, triggering_message_id, frame_timeout_ms)
        VALUES ($1, $2, 'running', now(), now(), $3, 60000)
    `, frameID, inst.ID, msgID)
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, active_terminal_at, run_scope_id)
        VALUES (gen_random_uuid(), $1, 'stub', ARRAY[]::text[], now(), 'failed', 'failed', $2, now(), $3)
    `, nodeRow.ID, frameID, mainScopeID)
	status, _ = h.httpJSON(t, "POST", "/v1/nodes/"+nodeRow.ID.String()+"/reset", nil)
	require.Equal(t, http.StatusOK, status)

	var loaded *persistence.NodeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Nodes().Get(ctx, nodeRow.ID, tx)
		loaded = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFailed, loaded.State)
	require.Nil(t, loaded.FrameID)

	// @story: node-admin
	// @decision: node-reset-as-pure-retry-budget-clear
	var resetFrameCount int
	pgtest.QueryRowForTest(ctx, t, h.driver, `
		SELECT count(*) FROM rimsky_frames f
		JOIN rimsky_messages m ON m.id = f.triggering_message_id
		WHERE f.instance_id = $1 AND m.type = 'node/reset'
	`, []any{inst.ID}, &resetFrameCount)
	require.Equal(t, 0, resetFrameCount,
		"post-spec the reset endpoint must not enqueue any node/reset frame")
	_ = nodeRow
}

func TestOperatorKill_RouteRemoved(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "op-kill-removed-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)

	status, _ := h.httpJSON(t, "POST", "/v1/nodes/"+nodeRow.ID.String()+"/kill", nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestEventsList(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "ev-"+uuid.NewString())
	nodeRow := firstNode(t, h, inst)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Events().Append(ctx, persistence.EventAppendInput{
			InstanceID: &inst.ID,
			NodeID:     &nodeRow.ID,
			Kind:       events.KindOperatorOverride(),
			Payload:    map[string]any{"action": "ev-test"},
		}, tx)
	}))

	status, out := h.httpJSON(t, "GET", "/v1/events?node_id="+nodeRow.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	gotEvents, _ := out["events"].([]any)
	require.Greater(t, len(gotEvents), 0)
}

func TestHealth(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", "/v1/health", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "ok", out["status"])
	counts, ok := out["node_counts"].(map[string]any)
	require.True(t, ok)
	for _, k := range []string{"fresh", "stale", "running", "failed"} {
		_, present := counts[k]
		require.True(t, present, "missing count key %q", k)
	}
}

func TestClaimHoldersRoute_EmptyList(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", "/v1/lock-holders/"+uuid.NewString()+"/claim-holders", nil)
	require.Equal(t, http.StatusOK, status, out)
	holders, _ := out["holders"].([]any)
	require.Empty(t, holders)
}

func seedInstance(t *testing.T, h *harness, tplName string) persistence.InstanceRow {
	t.Helper()
	ctx := context.Background()
	_, out := h.httpJSON(t, "POST", "/v1/templates", validTemplateBody(tplName))
	tplID := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	ck := "ck-" + uuid.NewString()
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
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
