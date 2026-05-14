// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// nodes_list_running_by_supervisor.go — NodesListRunningBySupervisor
// conformance area.
//
// Covers NodeTable.ListRunningBySupervisor — the supervisor-scoped
// running-nodes query the supervisor's heartbeat tick uses to refresh
// rimsky_nodes.last_heartbeat_at and to compute the supervisor's
// active_node_count. The DB is the source of truth (replacing an
// in-memory `activeNodes` map that missed async dispatches between
// AwaitAsyncCallback and the terminal callback). Both drivers must
// implement the predicate identically: state='running' AND
// assigned_supervisor_id = $1.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

func testNodesListRunningBySupervisor(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// Four sibling nodes against the same instance:
	//   - runningSelf:   state=running, supervisor=sup-A — must surface for sup-A.
	//   - runningOther:  state=running, supervisor=sup-B — must surface for sup-B only.
	//   - staleSelf:     state=stale,   supervisor=sup-A — state predicate excludes.
	//   - runningUnassigned: state=running, supervisor='' — supervisor predicate excludes (no string match).
	runningSelfID := shared.UUID(uuid.New())
	runningOtherID := shared.UUID(uuid.New())
	staleSelfID := shared.UUID(uuid.New())
	runningUnassignedID := shared.UUID(uuid.New())

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		for _, id := range []shared.UUID{runningSelfID, runningOtherID, staleSelfID, runningUnassignedID} {
			if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID:           id,
				InstanceID:   fix.InstanceID,
				NodeType:     "lrbs",
				Executor:     "test-executor",
				Dependencies: []shared.UUID{},
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}

	// Drive each row through the legal cascade transitions. UpdateHeartbeat
	// stamps assigned_supervisor_id when the supervisorID arg is non-empty.
	transitionToRunning := func(id shared.UUID, supervisorID string) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			if err := store.Nodes().UpdateState(ctx, id,
				cascade.NodeStateStale, cascade.ReasonOperatorInvalidate, "", tx); err != nil {
				return err
			}
			if err := store.Nodes().UpdateState(ctx, id,
				cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, "", tx); err != nil {
				return err
			}
			return store.Nodes().UpdateHeartbeat(ctx, id, time.Now(), supervisorID, tx)
		}); err != nil {
			t.Fatalf("transitionToRunning(%s, %q): %v", id, supervisorID, err)
		}
	}
	transitionToRunning(runningSelfID, "sup-A")
	transitionToRunning(runningOtherID, "sup-B")
	transitionToRunning(runningUnassignedID, "")

	// staleSelf: fresh -> stale (no run), supervisor stamped via heartbeat.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Nodes().UpdateState(ctx, staleSelfID,
			cascade.NodeStateStale, cascade.ReasonOperatorInvalidate, "", tx); err != nil {
			return err
		}
		return store.Nodes().UpdateHeartbeat(ctx, staleSelfID, time.Now(), "sup-A", tx)
	}); err != nil {
		t.Fatalf("seed staleSelf: %v", err)
	}

	// sup-A: must return exactly runningSelf.
	var got []persistence.NodeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.Nodes().ListRunningBySupervisor(ctx, "sup-A", tx)
		got = rows
		return err
	}); err != nil {
		t.Fatalf("ListRunningBySupervisor(sup-A): %v", err)
	}
	if len(got) != 1 || got[0].ID != runningSelfID {
		t.Fatalf("ListRunningBySupervisor(sup-A): got %v, want exactly [%s]", nodeIDStrings(got), runningSelfID)
	}
	if got[0].State != cascade.NodeStateRunning {
		t.Fatalf("ListRunningBySupervisor(sup-A): row state=%q want running", got[0].State)
	}
	if got[0].AssignedSupervisorID != "sup-A" {
		t.Fatalf("ListRunningBySupervisor(sup-A): row supervisor=%q want sup-A", got[0].AssignedSupervisorID)
	}

	// sup-B: must return exactly runningOther.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.Nodes().ListRunningBySupervisor(ctx, "sup-B", tx)
		got = rows
		return err
	}); err != nil {
		t.Fatalf("ListRunningBySupervisor(sup-B): %v", err)
	}
	if len(got) != 1 || got[0].ID != runningOtherID {
		t.Fatalf("ListRunningBySupervisor(sup-B): got %v, want exactly [%s]", nodeIDStrings(got), runningOtherID)
	}

	// Unknown supervisor: empty.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.Nodes().ListRunningBySupervisor(ctx, "sup-zzz", tx)
		got = rows
		return err
	}); err != nil {
		t.Fatalf("ListRunningBySupervisor(sup-zzz): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListRunningBySupervisor(sup-zzz): got %d rows, want 0", len(got))
	}
}

func nodeIDStrings(rows []persistence.NodeRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID.String())
	}
	return out
}
