// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: wait-set
// @concept: cascade

// @constraint: ListInFlightRunPhases conformance area.
// Covers Queue.ListInFlightRunPhases, the persistence half of the
// supervisor's upstream-gating eligibility condition: a stale receiver
// is not dispatch-eligible while any subscribed upstream has an
// in-flight run in the same (frame, run scope), and the gate's
// pending-cycle tie-breaker distinguishes a merely-pending upstream
// from a progressing one. Both drivers must agree on: keying (node set
// + frame + scope), counting only in-flight phases (pending / active /
// held / parked) — settled rows, whether deleted or transitioned to a
// settled phase, no longer gate — per-node phase reporting, and the
// empty-set fast path.
//
// @concept: wait-set
// @concept: cascade
package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// testListInFlightRunPhases_PerNodePhases: parity area for
// Queue.ListInFlightRunPhases (the per-node phase breakdown the
// upstream gate and its pending-cycle tie-breaker consume). Both
// drivers must agree on: keying (node set + frame + scope), the
// in-flight phase filter, per-node phase reporting, the empty-set
// fast path, absence for nodes with no in-flight rows, and the
// settlement release (a settled row stops reporting immediately).
func testListInFlightRunPhases_PerNodePhases(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	list := func(name string, nodeIDs []shared.UUID, frameID, scopeID shared.UUID) map[shared.UUID][]string {
		t.Helper()
		var got map[shared.UUID][]string
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			m, err := q.ListInFlightRunPhases(ctx, tx, nodeIDs, frameID, scopeID)
			got = m
			return err
		}); err != nil {
			t.Fatalf("%s: ListInFlightRunPhases: %v", name, err)
		}
		return got
	}

	m := list("match", []shared.UUID{fix.NodeID}, fix.FrameID, fix.MainRunScopeID)
	phases, ok := m[fix.NodeID]
	if !ok || len(phases) != 1 || phases[0] != "pending" {
		t.Errorf("match: phases for node = %v (present=%v), want [pending]", phases, ok)
	}

	// @deliberate: perturb each key dimension (frame / scope / node) in isolation; each must yield an empty map, proving the lookup keys on the full tuple.
	otherNode := shared.UUID(uuid.New())
	otherFrame := shared.UUID(uuid.New())
	otherScope := shared.UUID(uuid.New())
	if m := list("wrong-frame", []shared.UUID{fix.NodeID}, otherFrame, fix.MainRunScopeID); len(m) != 0 {
		t.Errorf("wrong-frame: got %v, want empty", m)
	}
	if m := list("wrong-scope", []shared.UUID{fix.NodeID}, fix.FrameID, otherScope); len(m) != 0 {
		t.Errorf("wrong-scope: got %v, want empty", m)
	}
	if m := list("other-node", []shared.UUID{otherNode}, fix.FrameID, fix.MainRunScopeID); len(m) != 0 {
		t.Errorf("other-node: got %v, want empty", m)
	}

	if m := list("empty-set", nil, fix.FrameID, fix.MainRunScopeID); len(m) != 0 {
		t.Errorf("empty-set: got %v, want empty", m)
	}

	// @constraint: a settled row (terminal phase deletes the dispatch row) must drop out of the in-flight report immediately so the upstream gate releases.
	if err := q.Complete(ctx, runID, ""); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if m := list("after-settle", []shared.UUID{fix.NodeID}, fix.FrameID, fix.MainRunScopeID); len(m) != 0 {
		t.Errorf("after-settle: got %v, want empty (settled rows must not gate)", m)
	}
}
