// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: debug-channel

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func pauseInstanceForTest(t *testing.T, h *harness, instanceID shared.UUID) {
	t.Helper()
	require.NoError(t, h.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		_, err := h.persist.Instances().SetPaused(ctx, instanceID, true, tx)
		return err
	}))
}

func seedPauseModeHitForTest(t *testing.T, h *harness, instanceID shared.UUID) {
	t.Helper()
	ctx := context.Background()
	frameID := seedRunningFrameForTest(ctx, t, h, instanceID)
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		bpID, err := h.persist.Breakpoints().Create(ctx, persistence.BreakpointRow{
			InstanceID:     instanceID,
			Matcher:        map[string]any{"node_type": "root"},
			Checkpoint:     persistence.CheckpointBeforeDispatch,
			Mode:           persistence.BreakpointModePause,
			OverflowPolicy: persistence.OverflowBlockDispatch,
			HitTTLSeconds:  300,
			CreatedByKey:   "test-seed",
		}, tx)
		if err != nil {
			return err
		}
		frameRow, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, frameRow)
		var rootNodeID shared.UUID
		nodes, err := h.persist.Nodes().ListByInstance(ctx, instanceID, tx)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if n.NodeType == "root" {
				rootNodeID = n.ID
				break
			}
		}
		if rootNodeID == (shared.UUID{}) {
			return fmt.Errorf("seedPauseModeHitForTest: no root node on instance %s", instanceID)
		}
		if _, err := h.persist.Nodes().CreateCascadePending(ctx, tx, rootNodeID, frameRow.RootRunScopeID, frameID); err != nil {
			return err
		}
		fresh, err := h.persist.Nodes().Get(ctx, rootNodeID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, fresh)
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNodeID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest, "seedPauseModeHitForTest: expected in-flight run row after Affirm")
		runID := latest.NodeRunID
		_, _, err = h.persist.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
			BreakpointID: bpID,
			InstanceID:   instanceID,
			NodeRunID:    &runID,
			FrameID:      &frameID,
			Checkpoint:   persistence.CheckpointBeforeDispatch,
			Mode:         persistence.BreakpointModePause,
			Snapshot:     map[string]any{"reason": "test-seed"},
		}, tx)
		return err
	}))
}

func findNodeIDByType(t *testing.T, h *harness, instanceID shared.UUID, typ string) persistence.NodeRow {
	t.Helper()
	var found *persistence.NodeRow
	require.NoError(t, h.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, instanceID, tx)
		if err != nil {
			return err
		}
		for i := range nodes {
			if nodes[i].NodeType == typ {
				n := nodes[i]
				found = &n
				return nil
			}
		}
		return nil
	}))
	require.NotNil(t, found, "expected to find node of type %q on instance %s", typ, instanceID)
	return *found
}

func seedRunningFrameForTest(ctx context.Context, t *testing.T, h *harness, instanceID shared.UUID) shared.UUID {
	t.Helper()
	msgID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_messages
            (id, instance_id, type, sender_kind, sender, payload, received_at)
        VALUES ($1, $2, '', 'operator', 'test', E'{}'::bytea, now())
    `, msgID, instanceID)
	rootScope := mainRunScopeIDForInstance(t, h, instanceID)
	frameID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, started_at, triggering_message_id, root_run_scope_id)
        VALUES ($1, $2, now(), $3, $4)
    `, frameID, instanceID, msgID, rootScope)
	return shared.UUID(frameID)
}

func hasDebugOverrideAuditEvent(t *testing.T, h *harness, instanceID shared.UUID) bool {
	t.Helper()
	var found bool
	require.NoError(t, h.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		out, err := h.persist.Events().List(ctx, persistence.EventListFilter{
			InstanceID: &instanceID,
			Kind:       "debug.override.applied",
		}, persistence.ListPagination{Limit: 10}, tx)
		if err != nil {
			return err
		}
		found = len(out.Events) > 0
		return nil
	}))
	return found
}

func TestDebugOverride_HealthyInstanceRefusedWith409(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "debug-healthy")
	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "invalidate_node",
		"node_type": "root",
	})
	require.Equal(t, http.StatusConflict, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), "not in debuggable state")
	states, _ := out["states"].([]any)
	require.ElementsMatch(t, []any{"paused", "breakpoint"}, states)
}

func TestDebugOverride_PausedInstanceAcceptedInvalidateNode(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "debug-paused")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "invalidate_node",
		"node_type": "root",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "paused", out["gate_state"])
	require.True(t, hasDebugOverrideAuditEvent(t, h, instUUID),
		"audit log must carry the debug.override.applied row after a successful override")
}

func TestDebugOverride_BreakpointHitAcceptedSetAttribute(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "debug-bp")
	instUUID := mustParseUUID(t, instID)
	seedPauseModeHitForTest(t, h, instUUID)

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":          "set_attribute",
		"node_type":       "root",
		"attribute_key":   "override_key",
		"attribute_value": "override_value",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "breakpoint", out["gate_state"])
	require.True(t, hasDebugOverrideAuditEvent(t, h, instUUID),
		"audit log must carry the debug.override.applied row after a successful override")
}

func TestDebugOverride_BodyValidation_RejectsAtBoundary(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "debug-body")

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"node_type": "root",
	})
	require.Equal(t, http.StatusBadRequest, status, out)

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "kaboom",
		"node_type": "root",
	})
	require.Equal(t, http.StatusBadRequest, status, out)

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action": "invalidate_node",
	})
	require.Equal(t, http.StatusBadRequest, status, out)

	status, out = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "set_attribute",
		"node_type": "root",
	})
	require.Equal(t, http.StatusBadRequest, status, out)
}

func TestDebugOverride_UnknownInstance404(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	bogus := uuid.NewString()
	status, _ := h.httpJSON(t, "POST", "/v1/instances/"+bogus+"/debug/override", map[string]any{
		"action":    "invalidate_node",
		"node_type": "root",
	})
	require.Equal(t, http.StatusNotFound, status)
}

func TestDebugOverride_InvalidateNodeMutatesNodeRun(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-mut")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")

	frameID := seedRunningFrameForTest(ctx, t, h, instUUID)

	var inFlightRunID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		frameRow, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, frameRow)
		if _, err := h.persist.Nodes().CreateCascadePending(ctx, tx, rootNode.ID, frameRow.RootRunScopeID, frameID); err != nil {
			return err
		}
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		if latest == nil {
			return fmt.Errorf("expected in-flight run row after Affirm")
		}
		inFlightRunID = latest.NodeRunID
		return nil
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "invalidate_node",
		"node_type": "root",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.GreaterOrEqual(t, int(out["runs_mutated"].(float64)), 1)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_ = inFlightRunID
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest)
		require.Equal(t, cascade.NodeStateStale, latest.State,
			"invalidate_node must produce a stale node-run")
		return nil
	}))
}

func TestDebugOverride_SetAttributeWritesAttribute(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-attr")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")

	frameID := seedRunningFrameForTest(ctx, t, h, instUUID)

	var inFlightRunID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		frameRow, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, frameRow)
		if _, err := h.persist.Nodes().CreateCascadePending(ctx, tx, rootNode.ID, frameRow.RootRunScopeID, frameID); err != nil {
			return err
		}
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest)
		inFlightRunID = latest.NodeRunID
		return h.persist.NodeAttributes().Upsert(ctx, inFlightRunID, rootNode.ID, map[string]any{"seed": "yes"}, tx)
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":          "set_attribute",
		"node_type":       "root",
		"attribute_key":   "override_key",
		"attribute_value": "override_value",
	})
	require.Equal(t, http.StatusOK, status, out)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.NodeAttributes().GetByRun(ctx, inFlightRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		require.Equal(t, "override_value", row.Data["override_key"],
			"set_attribute must write the attribute key/value into the run's attribute row")
		require.Equal(t, "yes", row.Data["seed"])
		return nil
	}))
}

func TestDebugOverride_SetAttributeNoInFlightRunIsNoOp(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-attr-no-inflight")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		require.Nil(t, latest, "test precondition: the root node must have no run row")
		return nil
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":          "set_attribute",
		"node_type":       "root",
		"attribute_key":   "override_key",
		"attribute_value": "override_value",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.True(t, hasDebugOverrideAuditEvent(t, h, instUUID),
		"audit log must carry the debug.override.applied row even when no run was mutated")
	require.EqualValues(t, 0, out["runs_mutated"],
		"set_attribute with no in-flight run must not mutate any run")

	rootScope := mainRunScopeIDForInstance(t, h, instUUID)
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := h.persist.NodeAttributes().GetLatestByNode(ctx, rootNode.ID, rootScope, tx)
		if err != nil {
			return err
		}
		require.Nil(t, latest,
			"no attribute row may materialize for the node when set_attribute hits the no-in-flight-run case")
		return nil
	}))
}

func seedTerminalRunUnderEndedFrame(
	ctx context.Context, t *testing.T, h *harness,
	instanceID shared.UUID, nodeID shared.UUID,
) (runScopeID shared.UUID, frameID shared.UUID) {
	t.Helper()
	runScopeID = shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_run_scopes(id, graph_name, instance_id, partition_key, created_at)
        VALUES ($1, 'main', $2, '', now())
    `, uuid.UUID(runScopeID), instanceID)
	msgID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_messages
            (id, instance_id, type, sender_kind, sender, payload, received_at)
        VALUES ($1, $2, '', 'operator', 'test', E'{}'::bytea, now())
    `, msgID, instanceID)
	frameID = shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_frames
            (frame_id, instance_id, ended_at, triggering_message_id, root_run_scope_id)
        VALUES ($1, $2, now(), $3, $4)
    `, uuid.UUID(frameID), instanceID, msgID, uuid.UUID(runScopeID))

	runID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, frame_id, active_terminal_at, run_scope_id, sequence)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], now(), 'fresh', $3, now(), $4, 0)
    `, runID, uuid.UUID(nodeID), uuid.UUID(frameID), uuid.UUID(runScopeID))
	sig := "terminal/success"
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.NodeRunTree().UpdateStateAndOutcome(ctx, tx, runID, cascade.NodeStateFresh, &sig, false)
	}))
	return runScopeID, frameID
}

func TestDebugOverride_InvalidateNodeRefusesCrossFramePairing(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-mut-prior-frame")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")
	_, priorFrameID := seedTerminalRunUnderEndedFrame(ctx, t, h, instUUID, rootNode.ID)

	var priorRunID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest)
		require.Equal(t, priorFrameID, latest.FrameID, "test precondition: latest run belongs to the ended prior frame")
		priorRunID = latest.NodeRunID
		return nil
	}))

	currentFrameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/debug-mut-current")

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "invalidate_node",
		"node_type": "root",
	})
	require.Equal(t, http.StatusConflict, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), priorFrameID.String())
	require.Contains(t, fmt.Sprint(out["error"]), currentFrameID.String())
	require.Equal(t, rootNode.ID.String(), out["node_id"])
	require.Equal(t, "root", out["node_type"])
	require.False(t, hasDebugOverrideAuditEvent(t, h, instUUID),
		"a refused cross-frame invalidate must not be audited as an applied override")

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest)
		require.Equal(t, priorRunID, latest.NodeRunID,
			"a refused cross-frame invalidate must not create any new node-run")
		require.Equal(t, cascade.NodeStateFresh, latest.State,
			"the prior frame's terminal run must remain untouched")
		return nil
	}))
}

func TestDebugOverride_SetAttributeRefusesCrossFramePairing(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-attr-prior-frame")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")
	_, priorFrameID := seedTerminalRunUnderEndedFrame(ctx, t, h, instUUID, rootNode.ID)

	var priorRunID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest)
		require.Equal(t, priorFrameID, latest.FrameID, "test precondition: latest run belongs to the ended prior frame")
		priorRunID = latest.NodeRunID
		return h.persist.NodeAttributes().Upsert(ctx, priorRunID, rootNode.ID, map[string]any{"seed": "prior"}, tx)
	}))

	currentFrameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/debug-attr-current")

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":          "set_attribute",
		"node_type":       "root",
		"attribute_key":   "override_key",
		"attribute_value": "override_value",
	})
	require.Equal(t, http.StatusConflict, status, out)
	require.Contains(t, fmt.Sprint(out["error"]), priorFrameID.String())
	require.Contains(t, fmt.Sprint(out["error"]), currentFrameID.String())
	require.False(t, hasDebugOverrideAuditEvent(t, h, instUUID),
		"a refused cross-frame set_attribute must not be audited as an applied override")

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.NodeAttributes().GetByRun(ctx, priorRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		require.NotContains(t, row.Data, "override_key",
			"a refused cross-frame set_attribute must not write into the prior frame's terminal run")
		require.Equal(t, "prior", row.Data["seed"])
		return nil
	}))
}

func seedRunInActiveFrame(
	ctx context.Context, t *testing.T, h *harness,
	nodeID, frameID, runScopeID shared.UUID, state cascade.NodeState,
) shared.UUID {
	t.Helper()
	runID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, frame_id, run_scope_id, sequence)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], now(), $3, $4, $5, 0)
    `, uuid.UUID(runID), uuid.UUID(nodeID), string(state), uuid.UUID(frameID), uuid.UUID(runScopeID))
	return runID
}

func rootRunScopeForFrame(t *testing.T, ctx context.Context, h *harness, frameID shared.UUID) shared.UUID {
	t.Helper()
	var rootScope shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		frameRow, err := h.persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, frameRow)
		rootScope = frameRow.RootRunScopeID
		return nil
	}))
	return rootScope
}

func TestDebugOverride_InvalidateNodePausedFrameRunIsLegalTarget(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-mut-paused-frame")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")
	currentFrameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/debug-mut-held")
	rootScope := rootRunScopeForFrame(t, ctx, h, currentFrameID)
	heldRunID := seedRunInActiveFrame(ctx, t, h, rootNode.ID, currentFrameID, rootScope, cascade.NodeStateHeld)

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "invalidate_node",
		"node_type": "root",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 1, out["runs_mutated"],
		"a held (paused) run in the active frame must remain a legal invalidate target")

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest)
		require.NotEqual(t, heldRunID, latest.NodeRunID,
			"invalidate_node must create a new run rather than mutating the held run in place")
		require.Equal(t, cascade.NodeStateStale, latest.State)
		require.Equal(t, currentFrameID, latest.FrameID)
		require.Equal(t, rootScope, latest.RunScopeID)
		return nil
	}))
}

func TestDebugOverride_SetAttributePausedFrameRunIsLegalTarget(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-attr-paused-frame")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")
	currentFrameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/debug-attr-parked")
	rootScope := rootRunScopeForFrame(t, ctx, h, currentFrameID)
	parkedRunID := seedRunInActiveFrame(ctx, t, h, rootNode.ID, currentFrameID, rootScope, cascade.NodeStateParked)

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":          "set_attribute",
		"node_type":       "root",
		"attribute_key":   "override_key",
		"attribute_value": "override_value",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 1, out["runs_mutated"],
		"a parked (paused) run in the active frame must remain a legal set_attribute target")

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.persist.NodeAttributes().GetByRun(ctx, parkedRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		require.Equal(t, "override_value", row.Data["override_key"],
			"a non-terminal run in the active frame accepts a direct attribute write, no redirect needed")
		return nil
	}))
}

func TestDebugOverride_SetAttributeNeverMutatesInFrameTerminalRun(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-attr-terminal-in-frame")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")
	currentFrameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/debug-attr-terminal")
	rootScope := rootRunScopeForFrame(t, ctx, h, currentFrameID)
	terminalRunID := seedRunInActiveFrame(ctx, t, h, rootNode.ID, currentFrameID, rootScope, cascade.NodeStateFresh)
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.NodeAttributes().Upsert(ctx, terminalRunID, rootNode.ID, map[string]any{"seed": "terminal"}, tx)
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":          "set_attribute",
		"node_type":       "root",
		"attribute_key":   "override_key",
		"attribute_value": "override_value",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 1, out["runs_mutated"])

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		terminalRow, err := h.persist.NodeAttributes().GetByRun(ctx, terminalRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, terminalRow)
		require.NotContains(t, terminalRow.Data, "override_key",
			"a terminal run must never be mutated by set_attribute, even inside the active frame")

		latest, err := h.persist.Nodes().GetLatestRunForNode(ctx, tx, rootNode.ID)
		if err != nil {
			return err
		}
		require.NotNil(t, latest)
		require.NotEqual(t, terminalRunID, latest.NodeRunID)
		require.Equal(t, cascade.NodeStateStale, latest.State)
		require.Equal(t, currentFrameID, latest.FrameID)
		require.Equal(t, rootScope, latest.RunScopeID)

		freshRow, err := h.persist.NodeAttributes().GetByRun(ctx, latest.NodeRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, freshRow)
		require.Equal(t, "override_value", freshRow.Data["override_key"],
			"set_attribute must land its value on the freshly invalidated node-run")
		require.Equal(t, "terminal", freshRow.Data["seed"],
			"the freshly invalidated run must carry forward the terminal run's prior bag")
		return nil
	}))
}

func TestDebugOverride_UnknownNodeTypeIsNoOpButStillAudited(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "debug-unknown-type")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "invalidate_node",
		"node_type": "nonexistent",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 0, out["runs_mutated"],
		"an unknown node_type must match no nodes and mutate nothing")
	require.True(t, hasDebugOverrideAuditEvent(t, h, instUUID),
		"the no-op must still be audited via debug.override.applied")
}

func TestDebugOverride_TOCTOU_GateAndMutationShareTx(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	const iterations = 12
	for i := 0; i < iterations; i++ {
		instID := newInstanceForMessages(t, h, fmt.Sprintf("debug-toctou-%d", i))
		instUUID := mustParseUUID(t, instID)
		pauseInstanceForTest(t, h, instUUID)

		var (
			wg     sync.WaitGroup
			status int
			body   map[string]any
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			status, body = h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
				"action":    "invalidate_node",
				"node_type": "root",
			})
		}()
		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 250)
			_ = h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				_, err := h.persist.Instances().SetPaused(ctx, instUUID, false, tx)
				return err
			})
		}()
		wg.Wait()
		auditWritten := hasDebugOverrideAuditEvent(t, h, instUUID)
		switch status {
		case http.StatusOK:
			require.True(t, auditWritten,
				"iteration %d: override applied (200) but no audit row — gate/mutation tx atomicity broken", i)
		case http.StatusConflict:
			require.False(t, auditWritten,
				"iteration %d: gate refused (409) but audit row written — gate/mutation tx atomicity broken", i)
		default:
			t.Fatalf("iteration %d: unexpected status %d, body=%v", i, status, body)
		}
	}
}
