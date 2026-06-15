// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: conformance area conformance area.
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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testNodesListRunningBySupervisor(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	// @deliberate: four sibling nodes against the same instance exercise
	// the state='running' AND assigned_supervisor_id=$1 predicate from
	// every angle: runningSelf (running, sup-A) must surface for sup-A;
	// runningOther (running, sup-B) must surface for sup-B only;
	// staleSelf (stale, sup-A) the state predicate excludes;
	// runningUnassigned (running, '') the supervisor predicate excludes
	// (empty string never matches a real supervisor id).
	runningSelfID := shared.UUID(uuid.New())
	runningOtherID := shared.UUID(uuid.New())
	staleSelfID := shared.UUID(uuid.New())
	runningUnassignedID := shared.UUID(uuid.New())

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		for _, id := range []shared.UUID{runningSelfID, runningOtherID, staleSelfID, runningUnassignedID} {
			if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID:         id,
				InstanceID: fix.InstanceID,
				NodeType:   "lrbs",
				Executor:   "test-executor",
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}

	// @deliberate: post-stage-3 cutover, state lives on rimsky_node_runs.
	// Enqueue drives the row into pending+stale; ClaimDispatchRow flips
	// phase to active; UpdateState(running) writes state='running' on
	// the in-flight row.
	transitionToRunning := func(id shared.UUID, supervisorID string) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
				NodeID:         id,
				ExecutorName:   "test-executor",
				RequiredStores: []string{},
				EnqueuedAt:     time.Now().Add(-1 * time.Second),
				FrameID:        fix.FrameID,
				RunScopeID:     fix.MainRunScopeID,
			}, tx); err != nil {
				return err
			}
			if supervisorID != "" {
				cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
					AcceptedExecutors: []string{"test-executor"},
					AcceptedStores:    []string{},
					Limit:             100,
				})
				if err != nil {
					return err
				}
				for _, c := range cands {
					if c.NodeID != id {
						continue
					}
					ok, err := q.ClaimDispatchRow(ctx, tx, c.DispatchID, supervisorID)
					if err != nil {
						return err
					}
					if !ok {
						t.Fatalf("ClaimDispatchRow returned !ok for %s/%s", id, supervisorID)
					}
				}
			}
			if err := store.Nodes().UpdateState(ctx, id, fix.MainRunScopeID,
				cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx); err != nil {
				return err
			}
			return store.Nodes().UpdateHeartbeat(ctx, id, fix.MainRunScopeID, time.Now(), supervisorID, tx)
		}); err != nil {
			t.Fatalf("transitionToRunning(%s, %q): %v", id, supervisorID, err)
		}
	}
	transitionToRunning(runningSelfID, "sup-A")
	transitionToRunning(runningOtherID, "sup-B")
	transitionToRunning(runningUnassignedID, "")

	// @deliberate: staleSelf seeds a pending stale run row pinned to the
	// fixture frame; UpdateHeartbeat then stamps the supervisor.
	// Post-cutover `state='stale'` is the run row's state; the row stays
	// in phase='pending' (never claimed) so the state predicate excludes
	// it from ListRunningBySupervisor.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         staleSelfID,
			ExecutorName:   "test-executor",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-1 * time.Second),
			FrameID:        fix.FrameID,
			RunScopeID:     fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		return store.Nodes().UpdateHeartbeat(ctx, staleSelfID, fix.MainRunScopeID, time.Now(), "sup-A", tx)
	}); err != nil {
		t.Fatalf("seed staleSelf: %v", err)
	}

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
