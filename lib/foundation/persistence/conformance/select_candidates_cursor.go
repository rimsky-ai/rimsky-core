// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: conformance area conformance area.
// parity. CursorEnqueuedAfter / CursorAfterDispatchID page the
// selection ordering (enqueued_at, id); the runner uses the cursor to
// hop past a head-of-line batch whose candidates were all skipped in Go
// (e.g. the upstream in-flight gate). The two drivers implement the
// cursor by different mechanisms — postgres pushes a SQL tuple
// predicate (`enqueued_at > $6 OR (enqueued_at = $6 AND id > $7)`),
// sqlite compares in Go on the string-round-tripped timestamp — so
// the suite pins them to identical semantics:
//
//   - rows with EQUAL enqueued_at and distinct ids page by (enqueued_at,
//     id): the strict suffix after the cursor, no duplicates, no skips;
//   - a cursor beyond several rows (a fully-skipped batch) resumes at
//     the correct row;
//   - the zero-value cursor returns from the beginning;
//   - sub-second enqueued_at ordering is chronological (this pins the
//     sqlite driver's RFC3339 fixed-width formatting: a trailing-zero-
//     trimmed string would sort "…00.5Z" before "…00Z" lexicographically).
package conformance

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testSelectCandidatesKeysetCursor(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	q := d.Queue()
	fix := seedFixtureSet(ctx, t, d)

	// @deliberate: whole-second base time so the sub-second case below
	// is deterministic (t0 formats with no fractional digits under a
	// trailing-zero-trimming formatter — the shape that breaks
	// lexicographic ordering). One minute in the past keeps every row
	// inside the `enqueued_at <= now` dispatch window.
	t0 := time.Now().UTC().Add(-1 * time.Minute).Truncate(time.Second)

	// @deliberate: five rows — three with EQUAL enqueued_at (distinct
	// dispatch ids — the tie the cursor must break by id), one 500ms
	// later (sub-second ordering), one 2s later. The in-flight uniqueness
	// constraint is per (node, run_scope), so each row gets its own node
	// under the fixture instance; all share the fixture frame + main
	// RunScope.
	enqueueTimes := []time.Time{t0, t0, t0, t0.Add(500 * time.Millisecond), t0.Add(2 * time.Second)}
	for _, at := range enqueueTimes {
		nodeID := shared.UUID(uuid.New())
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID:         nodeID,
				InstanceID: fix.InstanceID,
				NodeType:   "fixture-node-type",
				Executor:   "test-executor",
			}, tx); err != nil {
				return err
			}
			return q.EnqueueInTx(ctx, persistence.DispatchRequest{
				NodeID:         nodeID,
				ExecutorName:   "test-executor",
				RequiredStores: []string{},
				EnqueuedAt:     at,
				FrameID:        fix.FrameID,
				RunScopeID:     fix.MainRunScopeID,
			}, tx)
		}); err != nil {
			t.Fatalf("seed cursor row at %v: %v", at, err)
		}
	}

	// @deliberate: selectPage runs SelectCandidates inside a rolled-back
	// tx so the FOR UPDATE locks release and the rows stay
	// pending+unclaimed across pages.
	probeErr := errors.New("rollback probe")
	selectPage := func(limit int, curAt time.Time, curID shared.UUID) []persistence.Candidate {
		t.Helper()
		var out []persistence.Candidate
		err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
				AcceptedExecutors:     []string{"test-executor"},
				AcceptedStores:        []string{},
				Limit:                 limit,
				CursorEnqueuedAfter:   curAt,
				CursorAfterDispatchID: curID,
			})
			if err != nil {
				return err
			}
			out = cands
			return probeErr
		})
		if err != nil && !errors.Is(err, probeErr) {
			t.Fatalf("SelectCandidates(cursor=%v/%s): %v", curAt, curID, err)
		}
		return out
	}

	// @constraint: zero-value cursor returns from the beginning, in
	// (enqueued_at, id) order.
	full := selectPage(100, time.Time{}, shared.UUID{})
	if len(full) != len(enqueueTimes) {
		t.Fatalf("zero-cursor select: got %d candidates, want %d", len(full), len(enqueueTimes))
	}
	for i := 1; i < len(full); i++ {
		prev, cur := full[i-1], full[i]
		if cur.EnqueuedAt.Before(prev.EnqueuedAt) {
			t.Fatalf("selection ordering violated at index %d: %v before %v",
				i, cur.EnqueuedAt, prev.EnqueuedAt)
		}
		if cur.EnqueuedAt.Equal(prev.EnqueuedAt) &&
			bytes.Compare(cur.DispatchID[:], prev.DispatchID[:]) <= 0 {
			t.Fatalf("equal-timestamp id tiebreak violated at index %d: %s after %s",
				i, cur.DispatchID, prev.DispatchID)
		}
	}
	// @constraint: three t0 rows lead, then t0+500ms, then t0+2s — pins
	// chronological (not string-lexicographic) sub-second ordering.
	for i := 0; i < 3; i++ {
		if !full[i].EnqueuedAt.Equal(t0) {
			t.Fatalf("row %d: enqueued_at=%v, want the equal-timestamp batch at %v", i, full[i].EnqueuedAt, t0)
		}
	}
	if !full[3].EnqueuedAt.Equal(t0.Add(500 * time.Millisecond)) {
		t.Fatalf("row 3: enqueued_at=%v, want %v (sub-second row)", full[3].EnqueuedAt, t0.Add(500*time.Millisecond))
	}
	if !full[4].EnqueuedAt.Equal(t0.Add(2 * time.Second)) {
		t.Fatalf("row 4: enqueued_at=%v, want %v", full[4].EnqueuedAt, t0.Add(2*time.Second))
	}

	sameCandidate := func(a, b persistence.Candidate) bool {
		return a.DispatchID == b.DispatchID && a.NodeID == b.NodeID && a.EnqueuedAt.Equal(b.EnqueuedAt)
	}
	assertSuffix := func(got, want []persistence.Candidate, op string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %d candidates, want %d", op, len(got), len(want))
		}
		for i := range want {
			if !sameCandidate(got[i], want[i]) {
				t.Fatalf("%s: candidate %d mismatch: got %s@%v want %s@%v",
					op, i, got[i].DispatchID, got[i].EnqueuedAt, want[i].DispatchID, want[i].EnqueuedAt)
			}
		}
	}

	// @constraint: cursor inside the equal-timestamp batch yields the
	// strict suffix by id — no duplicates, no skips.
	for i := range full {
		got := selectPage(100, full[i].EnqueuedAt, full[i].DispatchID)
		assertSuffix(got, full[i+1:], "suffix after row "+full[i].DispatchID.String())
	}

	// @constraint: limit-2 paging walks the whole set exactly once — the
	// equal-timestamp batch spans the first page boundary, so the id
	// tiebreak is what prevents both duplicates and skips.
	var paged []persistence.Candidate
	curAt, curID := time.Time{}, shared.UUID{}
	for {
		page := selectPage(2, curAt, curID)
		if len(page) == 0 {
			break
		}
		if len(page) > 2 {
			t.Fatalf("paging: page exceeded limit: %d", len(page))
		}
		paged = append(paged, page...)
		last := page[len(page)-1]
		curAt, curID = last.EnqueuedAt, last.DispatchID
		if len(paged) > len(full) {
			t.Fatalf("paging: returned more rows than exist (duplicate rows)")
		}
	}
	assertSuffix(paged, full, "limit-2 paging")

	// @constraint: cursor beyond a fully-skipped batch (the last
	// equal-timestamp row) resumes at the first later row.
	got := selectPage(100, full[2].EnqueuedAt, full[2].DispatchID)
	assertSuffix(got, full[3:], "skip equal-timestamp batch")

	// @constraint: cursor at the final row returns nothing.
	if got := selectPage(100, full[4].EnqueuedAt, full[4].DispatchID); len(got) != 0 {
		t.Fatalf("cursor past the last row returned %d candidates, want 0", len(got))
	}
}
