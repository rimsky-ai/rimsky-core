// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// debug_override_test.go — Pass 9 of the message-schema-layer plan.
//
// Handler-level coverage for POST /instances/{id}/debug/override:
//
//   - the route exists and reaches the handler
//   - the gate refuses a healthy instance (HTTP 409 with both predicate names)
//   - the gate accepts a paused instance and applies the override
//   - the gate accepts an instance holding an unresumed pause-mode
//     breakpoint hit
//   - the audit log carries the new debug.override.applied row after a
//     successful override
//   - invalidate_node stale-marks the in-flight run
//   - set_attribute writes the attribute to the latest attribute row
//
// The TOCTOU resistance test (Task 39) sits in this file too because
// the gate semantic the property protects is local to this handler.
//
// @concept: debug-channel

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
)

// pauseInstanceForTest toggles rimsky_instances.paused = true via the
// persistence layer. The HTTP /pause endpoint also works but pulls in
// the full instance-handler machinery; the direct persistence call is
// the same surface the debug-channel gate reads, so testing the gate
// against it pins the actual property the gate enforces.
func pauseInstanceForTest(t *testing.T, h *harness, instanceID shared.UUID) {
	t.Helper()
	require.NoError(t, h.persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		_, err := h.persist.Instances().SetPaused(ctx, instanceID, true, tx)
		return err
	}))
}

// seedPauseModeHitForTest seeds a pause-mode breakpoint + an unresumed
// pause-mode hit on the instance so the debug-channel gate sees the
// "breakpoint" predicate satisfied. The hit row is unresumed
// (resumed_at IS NULL) AND its `node_run_id` points at a freshly-
// allocated in-flight (`phase=pending`) node-run row — the gate's
// predicate now requires the hit to actually be blocking a runner
// (the node-run referenced by the hit must be in a non-terminal
// phase), so the seed must mirror the production shape including the
// runner-side row.
func seedPauseModeHitForTest(t *testing.T, h *harness, instanceID shared.UUID) {
	t.Helper()
	ctx := context.Background()
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
		// @constraint: seed a non-terminal node-run row so the gate
		// predicate's node-run join clears — the hit must be blocking
		// a runner; a row with no node_run_id, or one whose node_run
		// is completed/failed, is NOT a blocker.
		inst, err := h.persist.Instances().Get(ctx, instanceID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, inst)
		frameID, err := ensureRunningFrameForTest(ctx, h, instanceID, tx)
		if err != nil {
			return err
		}
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
		if err := h.persist.Nodes().AffirmNodeRunRow(ctx, rootNodeID, inst.MainRunScopeID, frameID, tx); err != nil {
			return err
		}
		fresh, err := h.persist.Nodes().Get(ctx, rootNodeID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, fresh)
		require.NotNil(t, fresh.InFlightRunID,
			"seedPauseModeHitForTest: expected in-flight run row after Affirm")
		runID := *fresh.InFlightRunID
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

// @agent-contract: findNodeIDByType returns the first node with
// NodeType == typ on the instance; tests use it to confirm
// invalidate_node mutated the matching node-run keyed off node_type.
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

// @agent-contract: ensureRunningFrameForTest returns the instance's
// running frame id, promoting a queued frame to running if none is
// running yet. The runtime engine isn't booted in this harness so a
// freshly-created instance has a queued root frame but no running
// one; tests that need to seed a node-run inside a frame call this.
func ensureRunningFrameForTest(ctx context.Context, h *harness, instanceID shared.UUID, tx persistence.Tx) (shared.UUID, error) {
	running, err := h.persist.Frames().GetRunningFrameID(ctx, instanceID, tx)
	if err != nil {
		return shared.UUID{}, err
	}
	if running != nil {
		return *running, nil
	}
	filter := persistence.FrameListFilter{InstanceID: &instanceID, State: persistence.FrameStateQueued}
	page, err := h.persist.Frames().ListForObservability(ctx, filter, persistence.ListPagination{Limit: 1}, tx)
	if err != nil {
		return shared.UUID{}, err
	}
	if len(page.Rows) == 0 {
		return shared.UUID{}, fmt.Errorf("ensureRunningFrameForTest: no queued frame to promote on instance %s", instanceID)
	}
	candidate := page.Rows[0].FrameID
	if _, err := h.persist.Frames().PromoteQueuedFrameToRunning(ctx, candidate, tx); err != nil {
		return shared.UUID{}, err
	}
	return candidate, nil
}

// hasDebugOverrideAuditEvent reports whether the instance has any
// rimsky_events row of kind="debug.override.applied". The audit row
// is load-bearing for the falsifier ("the audit log has no
// debug.override.applied row after a successful override").
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

// TestDebugOverride_HealthyInstanceRefusedWith409 pins the gate. A
// freshly-created instance is neither paused nor holding a pause-mode
// hit; the handler must refuse with HTTP 409 and the body must name
// both predicates so the operator sees what would have unlocked the
// override.
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

// TestDebugOverride_PausedInstanceAcceptedInvalidateNode pins the
// happy path on the `paused` gate leg. After pausing the instance and
// posting the override, the response is 200, the audit row is written,
// and a re-read of the audit log confirms it.
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

// TestDebugOverride_BreakpointHitAcceptedSetAttribute pins the happy
// path on the `breakpoint` gate leg with the set_attribute action.
// After seeding an unresumed pause-mode hit and posting the override
// with action=set_attribute, the response is 200 with gate_state
// "breakpoint" and the audit row is written.
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

// TestDebugOverride_BodyValidation_RejectsAtBoundary covers the
// pre-tx validation: missing action, unknown action, missing fields.
// These never reach the gate so a malformed-request 400 cannot be
// used to fingerprint which instances are paused.
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

// @deliberate: pins the not-found surface — an override on a
// non-existent instance returns 404, never 409, because the gate
// check requires a found row.
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

// @deliberate: pins the falsifier "invalidate_node does not
// stale-mark a node" — pause the instance, manually allocate an
// in-flight run row for `root` bound to a fresh frame, POST the
// override, confirm the run row's state transitions to `stale`.
func TestDebugOverride_InvalidateNodeMutatesNodeRun(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-mut")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	// @deliberate: seed an in-flight run row + frame so
	// MarkStaleForCascade has something to write against;
	// invalidate_node no-ops on nodes with no in-flight run by design.
	rootNode := findNodeIDByType(t, h, instUUID, "root")

	var inFlightRunID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := h.persist.Instances().Get(ctx, instUUID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, inst)
		frameID, err := ensureRunningFrameForTest(ctx, h, instUUID, tx)
		if err != nil {
			return err
		}
		if err := h.persist.Nodes().AffirmNodeRunRow(ctx, rootNode.ID, inst.MainRunScopeID, frameID, tx); err != nil {
			return err
		}
		fresh, err := h.persist.Nodes().Get(ctx, rootNode.ID, tx)
		if err != nil {
			return err
		}
		if fresh == nil || fresh.InFlightRunID == nil {
			return fmt.Errorf("expected in-flight run row after Affirm")
		}
		inFlightRunID = *fresh.InFlightRunID
		return nil
	}))

	status, out := h.httpJSON(t, "POST", "/v1/instances/"+instID+"/debug/override", map[string]any{
		"action":    "invalidate_node",
		"node_type": "root",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.GreaterOrEqual(t, int(out["runs_mutated"].(float64)), 1)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fresh, err := h.persist.Nodes().Get(ctx, rootNode.ID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, fresh)
		// @deliberate: re-read InFlightRunID may differ if a new row
		// has been allocated, but the seeded row should be staled.
		_ = inFlightRunID
		require.Equal(t, cascade.NodeStateStale, fresh.State,
			"invalidate_node must stale-mark the in-flight node-run")
		return nil
	}))
}

// @deliberate: pins the falsifier "set_attribute does not write the
// attribute" — the action targets the in-flight run's attribute row;
// seed an attribute row, POST the override, confirm the row carries
// the operator-supplied key.
func TestDebugOverride_SetAttributeWritesAttribute(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-attr")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")

	// @deliberate: seed an in-flight run + an attribute row so
	// MergeDelta has something to merge into.
	var inFlightRunID shared.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := h.persist.Instances().Get(ctx, instUUID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, inst)
		frameID, err := ensureRunningFrameForTest(ctx, h, instUUID, tx)
		if err != nil {
			return err
		}
		if err := h.persist.Nodes().AffirmNodeRunRow(ctx, rootNode.ID, inst.MainRunScopeID, frameID, tx); err != nil {
			return err
		}
		fresh, err := h.persist.Nodes().Get(ctx, rootNode.ID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, fresh.InFlightRunID)
		inFlightRunID = *fresh.InFlightRunID
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
		// @constraint: seed key must survive — MergeDelta is shallow
		// merge, not replace; pinning this catches a future
		// "replace instead of merge" regression.
		require.Equal(t, "yes", row.Data["seed"])
		return nil
	}))
}

// @deliberate: pins the resolution-scope rule (see
// `code:control/controlapi/debug_override.go::setNodeAttributeForDebugOverride`):
// when there is no in-flight run for the named node-type,
// set_attribute is a no-op and does NOT fall through to write into
// the latest attribute row in the main RunScope (the silent
// two-segment fallback the prior implementation carried). The
// request returns 200 + audit row (the attempt was gate-validated)
// but runs_mutated is zero — refusing the write keeps the gate's
// "the override applies in that frame" guarantee crisp.
func TestDebugOverride_SetAttributeNoInFlightRunIsNoOp(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "debug-attr-no-inflight")
	instUUID := mustParseUUID(t, instID)
	pauseInstanceForTest(t, h, instUUID)

	rootNode := findNodeIDByType(t, h, instUUID, "root")
	require.Nil(t, rootNode.InFlightRunID,
		"test precondition: the root node must have no in-flight run")

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

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := h.persist.Instances().Get(ctx, instUUID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, inst)
		latest, err := h.persist.NodeAttributes().GetLatestByNode(ctx, rootNode.ID, inst.MainRunScopeID, tx)
		if err != nil {
			return err
		}
		require.Nil(t, latest,
			"no attribute row may materialize for the node when set_attribute hits the no-in-flight-run case")
		return nil
	}))
}

// @deliberate: TOCTOU resistance — the gate-check and the mutation
// share the request tx, so an external `paused = false` toggle
// interleaved between them either fully applies (gate-check read
// wins) or fully rejects (interleaved write wins) — never a partial
// state. Two goroutines race: one POSTs the override against a
// paused instance, the other flips paused=false. Repeated to give
// the race a chance to interleave. If the gate and mutation didn't
// share a tx, an audit row would appear without the gate's
// paused=true having held — observable as a status/audit mismatch.
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
		// @deliberate: small sleep so the toggle lands after the
		// override request opens its tx; without it the toggle may
		// fire before the gate-check and the race never exercises.
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
