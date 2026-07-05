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
		runID := latest.RunID
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
            (frame_id, instance_id, state, started_at, triggering_message_id, root_run_scope_id, frame_timeout_ms)
        VALUES ($1, $2, 'running', now(), $3, $4, 60000)
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
		inFlightRunID = latest.RunID
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
		inFlightRunID = latest.RunID
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
