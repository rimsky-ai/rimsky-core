// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

// @decision: node-state-retired-from-operator-api
func TestNodeResponse_NeverSynthesizesSingleValueState(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(nodeResponse{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if strings.EqualFold(f.Name, "state") {
			t.Fatalf("nodeResponse must never carry a synthesized single-value state field; found %q", f.Name)
		}
		jsonTag := strings.Split(f.Tag.Get("json"), ",")[0]
		if strings.EqualFold(jsonTag, "state") {
			t.Fatalf("nodeResponse must never serialize a %q json key; field %q carries it", jsonTag, f.Name)
		}
	}
}

func TestGetNode_SettlingSignalType(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "node-signal-"+uuid.NewString())
	settledNode := firstNode(t, h, inst)

	status, listOut := h.httpJSON(t, "GET", "/v1/instances/"+inst.ID.String()+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, listOut)
	nodes, _ := listOut["nodes"].([]any)
	require.NotEmpty(t, nodes)
	var resolvedNodeID string
	for _, n := range nodes {
		row, _ := n.(map[string]any)
		if row["id"] == settledNode.ID.String() {
			resolvedNodeID = settledNode.ID.String()
		}
	}
	require.Equal(t, settledNode.ID.String(), resolvedNodeID,
		"settled node id must be discoverable via the instance-nodes listing")

	const wantSignalType = "terminal/success"
	runID := seedTerminalRunWithSignalType(ctx, t, h, inst, settledNode.ID, wantSignalType)
	require.NotEqual(t, uuid.Nil, runID)

	status, out := h.httpJSON(t, "GET", "/v1/nodes/"+settledNode.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, wantSignalType, out["settling_signal_type"],
		"node detail must carry the persisted settling signal type")

	freshInst := seedInstance(t, h, "node-fresh-"+uuid.NewString())
	freshNode := firstNode(t, h, freshInst)
	status, freshOut := h.httpJSON(t, "GET", "/v1/nodes/"+freshNode.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, freshOut)
	got, present := freshOut["settling_signal_type"]
	require.False(t, present && got != nil && got != "",
		"unsettled node must omit settling_signal_type, got %v", got)
}

func TestResolveInstance_FallsBackToKeyLookupForUUIDShapedKey(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("resolve-uuidkey-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)

	uuidShapedKey := uuid.NewString()
	status, createOut := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": uuidShapedKey,
	})
	require.Equal(t, http.StatusCreated, status, createOut)
	instanceID, _ := createOut["instance_id"].(string)
	require.NotEmpty(t, instanceID)
	require.NotEqual(t, uuidShapedKey, instanceID,
		"instance_key and instance_id must differ for this to exercise the id-miss-then-key-fallback path")

	status, getOut := h.httpJSON(t, "GET", "/v1/instances/"+uuidShapedKey, nil)
	require.Equal(t, http.StatusOK, status, getOut)
	require.Equal(t, instanceID, getOut["id"],
		"a UUID-shaped instance_key that misses the id lookup must fall back to key lookup")
}

func TestGetNode_ErrorBranches(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", "/v1/nodes/not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, status, out)

	status, out = h.httpJSON(t, "GET", "/v1/nodes/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNotFound, status, out)
}

func TestListInstanceNodes_CursorAndLimitPagination(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	inst := seedInstance(t, h, "node-page-"+uuid.NewString())
	for i := 0; i < 3; i++ {
		require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := h.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID:         shared.UUID(uuid.New()),
				InstanceID: inst.ID,
				NodeType:   "extra-node-type",
				Executor:   "test-executor",
			}, tx)
			return err
		}))
	}

	status, page1 := h.httpJSON(t, "GET", "/v1/instances/"+inst.ID.String()+"/nodes?limit=2", nil)
	require.Equal(t, http.StatusOK, status, page1)
	rows1, _ := page1["nodes"].([]any)
	require.Len(t, rows1, 2, "limit=2 must cap the page at 2 rows")
	cursor, _ := page1["next_cursor"].(string)
	require.NotEmpty(t, cursor, "a further page must be signaled via next_cursor")

	seen := map[string]bool{}
	for _, r := range rows1 {
		row, _ := r.(map[string]any)
		seen[row["id"].(string)] = true
	}

	status, page2 := h.httpJSON(t, "GET", "/v1/instances/"+inst.ID.String()+"/nodes?limit=2&cursor="+cursor, nil)
	require.Equal(t, http.StatusOK, status, page2)
	rows2, _ := page2["nodes"].([]any)
	require.NotEmpty(t, rows2, "cursor-continued page must return the remaining rows")
	for _, r := range rows2 {
		row, _ := r.(map[string]any)
		id, _ := row["id"].(string)
		require.False(t, seen[id], "cursor pagination must not repeat a row already seen on page 1: %s", id)
	}
}

func seedTerminalRunWithSignalType(
	ctx context.Context, t *testing.T, h *harness,
	inst persistence.InstanceRow, nodeID shared.UUID, signalType string,
) uuid.UUID {
	t.Helper()
	mainScopeID, _, frameID := seedRunScopeMessageFrame(ctx, t, h, uuid.UUID(inst.ID), true)

	runID := uuid.New()
	pgdbtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_claim_producers, enqueued_at, state, frame_id, active_terminal_at, run_scope_id, sequence)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], now(), 'fresh', $3, now(), $4, 0)
    `, runID, nodeID, frameID, mainScopeID)

	sig := signalType
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.NodeRunTree().UpdateStateAndOutcome(ctx, runID, cascade.NodeStateFresh, &sig, false, tx)
	}))
	return runID
}
