// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// retention_sweep.go — RetentionSweep conformance area.
//
// Pins the scheduler-tick retention sweeps' SELECTION predicates —
// what gets deleted and, just as load-bearing, what must SURVIVE:
//
//   - ClaimHandles.DeleteResolvedOlderThan: only resolved rows past
//     the cutoff go; the committed-durable asset surface and every
//     active row survive regardless of age (no-data-loss property:
//     a retention sweep must never release live ownership or a
//     durable asset).
//   - Frames.PruneTraceForRetention: only TERMINAL frames are
//     candidates under the lesser-of (count OR time) bound; in-flight
//     frames (queued/running) are never reaped; a reaped frame's
//     node_runs cascade away with it; both bounds disabled is a
//     strict no-op.
//
// Both sweeps are window/predicate-heavy SQL with driver-specific
// idioms (timestamptz vs RFC3339-text comparison, ROW_NUMBER
// partitions), so each observable is asserted identically on both
// drivers. (The message-idempotency TTL sweep is covered in
// message_idempotency.go; the lineage sweep in lineage.go.)
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const retentionSup = "retention-supervisor"

// seedResolvedHandle inserts a claim-scope handle with the given
// lifetime and, when terminal != "", Promotes it (stamping resolved_at
// = now and nulling the holder). Returns the handle id.
func seedResolvedHandle(
	ctx context.Context, t *testing.T, d persistence.Database, fix fixtureSet,
	lifetime spec.ClaimLifetime, terminal spec.ClaimHandleState,
) shared.UUID {
	t.Helper()
	store := d.Tables()
	producer := "retention-conformance-producer"
	id := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 id,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     json.RawMessage(`{"path":"/retention/` + id.String() + `"}`),
			HolderSupervisorID: retentionSup,
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(1 * time.Hour),
			Lifetime:           lifetime,
		}, tx); err != nil {
			return err
		}
		if terminal == "" {
			return nil
		}
		return store.ClaimHandles().Promote(ctx, id, retentionSup, terminal, tx)
	}); err != nil {
		t.Fatalf("seedResolvedHandle(%s,%s): %v", lifetime, terminal, err)
	}
	return id
}

// testRetentionClaimHandleSweep pins DeleteResolvedOlderThan's
// predicate: state ∈ {committed, abandoned}, durable-committed
// excluded, resolved_at strictly before the cutoff.
func testRetentionClaimHandleSweep(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	ch := d.Tables().ClaimHandles()

	committedSubgraph := seedResolvedHandle(ctx, t, d, fix, spec.ClaimLifetimeSubgraph, spec.ClaimHandleStateCommitted)
	abandonedSubgraph := seedResolvedHandle(ctx, t, d, fix, spec.ClaimLifetimeSubgraph, spec.ClaimHandleStateAbandoned)
	abandonedDurable := seedResolvedHandle(ctx, t, d, fix, spec.ClaimLifetimeDurable, spec.ClaimHandleStateAbandoned)
	committedDurable := seedResolvedHandle(ctx, t, d, fix, spec.ClaimLifetimeDurable, spec.ClaimHandleStateCommitted)
	activeHandle := seedResolvedHandle(ctx, t, d, fix, spec.ClaimLifetimeSubgraph, "")

	// Cutoff in the past: every row resolved just now is younger than
	// the cutoff — nothing goes (the cutoff predicate, isolated).
	deleted, err := ch.DeleteResolvedOlderThan(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("DeleteResolvedOlderThan(past): %v", err)
	}
	if deleted != 0 {
		t.Fatalf("past-cutoff sweep deleted %d rows, want 0", deleted)
	}

	// Cutoff in the future: exactly the three eligible rows go —
	// committed-subgraph, abandoned-subgraph, abandoned-durable. The
	// committed-durable asset row and the active row must survive (the
	// no-data-loss property this sweep is allowed to violate exactly
	// never).
	deleted, err = ch.DeleteResolvedOlderThan(ctx, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("DeleteResolvedOlderThan(future): %v", err)
	}
	if deleted != 3 {
		t.Fatalf("future-cutoff sweep deleted %d rows, want exactly 3", deleted)
	}
	assertHandlePresence := func(id shared.UUID, wantPresent bool, label string) {
		row := getGuardClaimHandle(ctx, t, d, id)
		if (row != nil) != wantPresent {
			t.Fatalf("%s: present=%v, want present=%v", label, row != nil, wantPresent)
		}
	}
	assertHandlePresence(committedSubgraph, false, "committed-subgraph")
	assertHandlePresence(abandonedSubgraph, false, "abandoned-subgraph")
	assertHandlePresence(abandonedDurable, false, "abandoned-durable")
	assertHandlePresence(committedDurable, true, "committed-durable (asset surface)")
	assertHandlePresence(activeHandle, true, "active")
}

// testRetentionFrameTracePrune pins PruneTraceForRetention: the
// terminal-only candidate set, the time bound, the count bound, the
// union (lesser-of) semantics, the both-disabled no-op, and the
// frame → node_run cascade.
func testRetentionFrameTracePrune(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	frames := d.Tables().Frames()
	q := d.Queue()

	// The schema allows at most one RUNNING frame per instance, so the
	// frames are driven sequentially: terminate the fixture frame, then
	// mint + terminate f1..f3 with strictly-ordered ended_at stamps (the
	// sleeps clear sqlite's stored text precision), then leave one fresh
	// frame RUNNING — it must survive every sweep below (in-flight
	// frames are exempt). f1 carries a node_run so the cascade is
	// observable.
	mintRunningFrame := func(label string) shared.UUID {
		var fid shared.UUID
		frameOp(ctx, t, d, "mint "+label, func(tx persistence.Tx) error {
			var err error
			fid, err = frames.EnqueueSerialFrame(ctx, fix.InstanceID, fix.NodeID, 600000, tx)
			if err != nil {
				return err
			}
			transitioned, err := frames.PromoteQueuedFrameToRunning(ctx, fid, tx)
			if err != nil {
				return err
			}
			if !transitioned {
				t.Fatalf("mint %s: promote did not transition", label)
			}
			return nil
		})
		return fid
	}
	terminate := func(fid shared.UUID, label string) {
		frameOp(ctx, t, d, "terminate "+label, func(tx persistence.Tx) error {
			transitioned, err := frames.MarkRunningFrameTerminal(ctx, fid, persistence.FrameStateCompleted, tx)
			if err != nil {
				return err
			}
			if !transitioned {
				t.Fatalf("terminate %s: frame did not transition", label)
			}
			return nil
		})
	}

	terminate(fix.FrameID, "fixture frame")
	time.Sleep(20 * time.Millisecond)
	f1 := mintRunningFrame("f1")
	runOnF1 := seedConformanceRunForNode(ctx, t, d, fix.NodeID, f1)
	terminate(f1, "f1")
	time.Sleep(20 * time.Millisecond)
	betweenF1F2 := time.Now()
	time.Sleep(20 * time.Millisecond)
	f2 := mintRunningFrame("f2")
	terminate(f2, "f2")
	time.Sleep(20 * time.Millisecond)
	f3 := mintRunningFrame("f3")
	terminate(f3, "f3")
	runningF := mintRunningFrame("running survivor")

	// Both bounds disabled: strict no-op.
	n, err := frames.PruneTraceForRetention(ctx, 0, time.Time{})
	if err != nil {
		t.Fatalf("PruneTraceForRetention(disabled): %v", err)
	}
	if n != 0 {
		t.Fatalf("disabled prune deleted %d frames, want 0", n)
	}

	// Union semantics: the count bound keeps all 4 terminal frames, but
	// the time bound (cutoff between f1 and f2) still reaps the fixture
	// frame and f1 — EITHER predicate suffices. The cascade takes f1's
	// node_run with it.
	n, err = frames.PruneTraceForRetention(ctx, 4, betweenF1F2)
	if err != nil {
		t.Fatalf("PruneTraceForRetention(time bound): %v", err)
	}
	if n != 2 {
		t.Fatalf("time-bound prune deleted %d frames, want exactly 2 (fixture frame + f1)", n)
	}
	frameOp(ctx, t, d, "f1 gone, f2/f3 alive", func(tx persistence.Tx) error {
		for _, fid := range []shared.UUID{fix.FrameID, f1} {
			if row, err := frames.GetForObservability(ctx, fid, tx); err != nil || row != nil {
				t.Fatalf("frame %s after time-bound prune: row=%v err=%v, want reaped", fid, row, err)
			}
		}
		for _, fid := range []shared.UUID{f2, f3, runningF} {
			row, err := frames.GetForObservability(ctx, fid, tx)
			if err != nil || row == nil {
				t.Fatalf("frame %s vanished under the time-bound prune: row=%v err=%v", fid, row, err)
			}
		}
		return nil
	})
	run, err := q.GetByID(ctx, runOnF1)
	if err != nil {
		t.Fatalf("GetByID(run on f1): %v", err)
	}
	if run != nil {
		t.Fatalf("node_run %s survived its frame's prune — cascade did not fire", runOnF1)
	}

	// Count bound alone: keep the 1 most-recent terminal frame (f3);
	// f2 is reaped, the running fixture frame is untouched.
	n, err = frames.PruneTraceForRetention(ctx, 1, time.Time{})
	if err != nil {
		t.Fatalf("PruneTraceForRetention(count bound): %v", err)
	}
	if n != 1 {
		t.Fatalf("count-bound prune deleted %d frames, want exactly 1 (f2)", n)
	}
	frameOp(ctx, t, d, "count-bound survivors", func(tx persistence.Tx) error {
		if row, err := frames.GetForObservability(ctx, f2, tx); err != nil || row != nil {
			t.Fatalf("f2 after count-bound prune: row=%v err=%v, want reaped", row, err)
		}
		if row, err := frames.GetForObservability(ctx, f3, tx); err != nil || row == nil {
			t.Fatalf("f3 (most recent terminal) reaped by count-bound prune: row=%v err=%v", row, err)
		}
		return nil
	})

	// Time bound with a far-future cutoff reaps every remaining terminal
	// frame — and STILL never touches the running frame.
	n, err = frames.PruneTraceForRetention(ctx, 0, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("PruneTraceForRetention(far future): %v", err)
	}
	if n != 1 {
		t.Fatalf("far-future prune deleted %d frames, want exactly 1 (f3)", n)
	}
	frameOp(ctx, t, d, "running frame survives everything", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, runningF, tx)
		if err != nil || row == nil || row.State != persistence.FrameStateRunning {
			t.Fatalf("running frame after sweeps: row=%+v err=%v, want state=running", row, err)
		}
		return nil
	})
}
