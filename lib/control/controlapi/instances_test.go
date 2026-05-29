// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// instances_test.go — HTTP-level integration tests for the
// instance force-terminate surface (POST /instances/{idOrKey}/terminate)
// per spec
// .ok-planner/specs/2026-05-28-quality-of-life-features-design.md
// Feature 2. Exercised against the pgtest harness (real Postgres via
// testcontainers).
//
// terminate is the first production instance-teardown path: it marks the
// instance terminal (sets terminated_at, previously only test-driven via
// MarkTerminated) and force-fails the instance's resource-holding
// in-flight node-runs under the new instance_killed transition reason.
//
// @concept: instance

package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

// seedRunningNodeRun replaces any in-flight runs for `node` with a single
// active/running run row keyed to the instance's main run-scope and an
// existing frame, mimicking a node mid-dispatch. Returns the run-scope id
// the row was seeded under.
func seedRunningNodeRun(t *testing.T, h *harness, inst persistence.InstanceRow, node persistence.NodeRow) {
	t.Helper()
	ctx := context.Background()

	// Drop any in-flight rows the create flow may have allocated so the
	// uq_node_runs_in_flight_per_run_scope unique index doesn't reject
	// the seeded running row.
	pgtest.ExecForTest(ctx, t, h.driver,
		`DELETE FROM rimsky_node_runs WHERE node_id=$1 AND phase IN ('pending','active','held','parked')`,
		node.ID)

	var frameID uuid.UUID
	pgtest.QueryRowForTest(ctx, t, h.driver,
		`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 ORDER BY queued_at DESC LIMIT 1`,
		[]any{inst.ID}, &frameID)

	pgtest.ExecForTest(ctx, t, h.driver, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, claimed_by, claimed_at, last_heartbeat_at, phase, state, frame_id, run_scope_id)
        VALUES (gen_random_uuid(), $1, 'worker', ARRAY[]::text[], now(), 'sup-1', now(), now(), 'active', 'running', $2, $3)
    `, node.ID, frameID, inst.MainRunScopeID)
}

// loadNodeState reads a node's projected state back through the
// persistence layer (the LATERAL/CASE projection over its in-flight or
// most-recent run row).
func loadNodeState(t *testing.T, h *harness, nodeID persistence.NodeRow) cascade.NodeState {
	t.Helper()
	ctx := context.Background()
	var loaded *persistence.NodeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.Nodes().Get(ctx, nodeID.ID, tx)
		loaded = r
		return err
	}))
	require.NotNil(t, loaded)
	return loaded.State
}

// loadRunScopeClosed reports whether the given run-scope's closed_at is
// set, reading it back through the persistence layer.
func loadRunScopeClosed(t *testing.T, h *harness, id foundationshared.UUID) bool {
	t.Helper()
	ctx := context.Background()
	var loaded *persistence.RunScopeRow
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.persist.RunScopes().GetByID(ctx, tx, id)
		loaded = r
		return err
	}))
	require.NotNil(t, loaded, "run-scope %s must exist", id)
	return loaded.ClosedAt != nil
}

// TestTerminateInstance_ForceFailsRunningNode is the Feature 2 happy
// path: terminate sets terminated_at, force-fails a running node-run to
// failed, records an instance_terminated event with the reason, and a
// subsequent DELETE now succeeds (the 409 terminal guard passes).
func TestTerminateInstance_ForceFailsRunningNode(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "term-run-"+uuid.NewString())
	node := firstNode(t, h, inst)
	seedRunningNodeRun(t, h, inst, node)
	require.Equal(t, cascade.NodeStateRunning, loadNodeState(t, h, node),
		"precondition: node-run must be running before terminate")

	// Terminate with a reason.
	status, out := h.httpJSON(t, "POST", "/instances/"+inst.ID.String()+"/terminate", map[string]any{
		"reason": "stuck-on-async-callback",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.NotEmpty(t, out["terminated_at"], "terminate must set terminated_at")

	// The running node-run is force-failed.
	require.Equal(t, cascade.NodeStateFailed, loadNodeState(t, h, node),
		"running node-run must be force-failed by terminate")

	// The instance's main run-scope is closed by terminate itself — not
	// left to the instance_terminator worker (whose sweep skips instances
	// with no lifecycle-subscriber rows, like this one).
	require.True(t, loadRunScopeClosed(t, h, inst.MainRunScopeID),
		"terminate must close the instance's main run-scope")

	// An instance_terminated event with the reason was recorded.
	status, out = h.httpJSON(t, "GET",
		fmt.Sprintf("/events?instance_id=%s&kind=instance_terminated", inst.ID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	events, _ := out["events"].([]any)
	require.Len(t, events, 1, "exactly one instance_terminated event expected")
	ev, _ := events[0].(map[string]any)
	payload, _ := ev["payload"].(map[string]any)
	require.Equal(t, "stuck-on-async-callback", payload["reason"])

	// DELETE now succeeds: the 409 terminal guard passes for a
	// terminated instance.
	status, _ = h.httpJSON(t, "DELETE", "/instances/"+inst.ID.String(), nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = h.httpJSON(t, "GET", "/instances/"+inst.ID.String(), nil)
	require.Equal(t, http.StatusNotFound, status)
}

// TestTerminateInstance_NoReasonEmptyBody confirms terminate tolerates an
// absent body (reason defaults to empty) and still marks terminal.
func TestTerminateInstance_NoReasonEmptyBody(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "term-nobody-"+uuid.NewString())

	status, out := h.httpJSON(t, "POST", "/instances/"+inst.ID.String()+"/terminate", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.NotEmpty(t, out["terminated_at"])

	status, out = h.httpJSON(t, "GET",
		fmt.Sprintf("/events?instance_id=%s&kind=instance_terminated", inst.ID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	events, _ := out["events"].([]any)
	require.Len(t, events, 1)
	ev, _ := events[0].(map[string]any)
	payload, _ := ev["payload"].(map[string]any)
	require.Equal(t, "", payload["reason"])
}

// TestTerminateInstance_Idempotent confirms a second terminate on an
// already-terminal instance returns 200 with no error and records no
// second event.
func TestTerminateInstance_Idempotent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	inst := seedInstance(t, h, "term-idem-"+uuid.NewString())

	status, out := h.httpJSON(t, "POST", "/instances/"+inst.ID.String()+"/terminate", map[string]any{
		"reason": "first",
	})
	require.Equal(t, http.StatusOK, status, out)
	require.NotEmpty(t, out["terminated_at"])

	// Second call is idempotent: still 200, terminated_at unchanged.
	status, out2 := h.httpJSON(t, "POST", "/instances/"+inst.ID.String()+"/terminate", map[string]any{
		"reason": "second",
	})
	require.Equal(t, http.StatusOK, status, out2)
	require.Equal(t, out["terminated_at"], out2["terminated_at"],
		"idempotent terminate must not move terminated_at")

	// Only the first call recorded an event.
	status, out3 := h.httpJSON(t, "GET",
		fmt.Sprintf("/events?instance_id=%s&kind=instance_terminated", inst.ID.String()), nil)
	require.Equal(t, http.StatusOK, status, out3)
	events, _ := out3["events"].([]any)
	require.Len(t, events, 1, "idempotent terminate must not append a second event")
}

// TestTerminateInstance_NotFound returns 404 for an unknown instance.
func TestTerminateInstance_NotFound(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, _ := h.httpJSON(t, "POST", "/instances/"+uuid.NewString()+"/terminate", nil)
	require.Equal(t, http.StatusNotFound, status)
}
