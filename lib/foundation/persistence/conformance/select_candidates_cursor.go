// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	t0 := time.Now().UTC().Add(-1 * time.Minute).Truncate(time.Second)

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
			return q.Enqueue(ctx, persistence.DispatchRequest{
				NodeID:                 nodeID,
				ExecutorName:           "test-executor",
				RequiredClaimProducers: []string{},
				EnqueuedAt:             at,
				FrameID:                fix.FrameID,
				RunScopeID:             fix.MainRunScopeID,
			}, tx)
		}); err != nil {
			t.Fatalf("seed cursor row at %v: %v", at, err)
		}
	}

	probeErr := errors.New("rollback probe")
	selectPage := func(limit int, curAt time.Time, curSeq int64, curID shared.UUID) []persistence.Candidate {
		t.Helper()
		var out []persistence.Candidate
		err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
				Limit:                limit,
				CursorEnqueuedAfter:  curAt,
				CursorAfterSequence:  curSeq,
				CursorAfterNodeRunID: curID,
			}, tx)
			if err != nil {
				return err
			}
			out = cands
			return probeErr
		})
		if err != nil && !errors.Is(err, probeErr) {
			t.Fatalf("SelectCandidates(cursor=%v/%d/%s): %v", curAt, curSeq, curID, err)
		}
		return out
	}

	full := selectPage(100, time.Time{}, 0, shared.UUID{})
	if len(full) != len(enqueueTimes) {
		t.Fatalf("zero-cursor select: got %d candidates, want %d", len(full), len(enqueueTimes))
	}
	for i := 1; i < len(full); i++ {
		prev, cur := full[i-1], full[i]
		if cur.EnqueuedAt.Before(prev.EnqueuedAt) {
			t.Fatalf("selection ordering violated at index %d: %v before %v",
				i, cur.EnqueuedAt, prev.EnqueuedAt)
		}
		if cur.EnqueuedAt.Equal(prev.EnqueuedAt) && cur.Sequence < prev.Sequence {
			t.Fatalf("equal-timestamp sequence tiebreak violated at index %d: sequence %d after %d",
				i, cur.Sequence, prev.Sequence)
		}
		if cur.EnqueuedAt.Equal(prev.EnqueuedAt) && cur.Sequence == prev.Sequence &&
			bytes.Compare(cur.NodeRunID[:], prev.NodeRunID[:]) <= 0 {
			t.Fatalf("equal-timestamp equal-sequence id tiebreak violated at index %d: %s after %s",
				i, cur.NodeRunID, prev.NodeRunID)
		}
	}
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
		return a.NodeRunID == b.NodeRunID && a.NodeID == b.NodeID && a.EnqueuedAt.Equal(b.EnqueuedAt)
	}
	assertSuffix := func(got, want []persistence.Candidate, op string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %d candidates, want %d", op, len(got), len(want))
		}
		for i := range want {
			if !sameCandidate(got[i], want[i]) {
				t.Fatalf("%s: candidate %d mismatch: got %s@%v want %s@%v",
					op, i, got[i].NodeRunID, got[i].EnqueuedAt, want[i].NodeRunID, want[i].EnqueuedAt)
			}
		}
	}

	for i := range full {
		got := selectPage(100, full[i].EnqueuedAt, full[i].Sequence, full[i].NodeRunID)
		assertSuffix(got, full[i+1:], "suffix after row "+full[i].NodeRunID.String())
	}

	var paged []persistence.Candidate
	curAt, curSeq, curID := time.Time{}, int64(0), shared.UUID{}
	for {
		page := selectPage(2, curAt, curSeq, curID)
		if len(page) == 0 {
			break
		}
		if len(page) > 2 {
			t.Fatalf("paging: page exceeded limit: %d", len(page))
		}
		paged = append(paged, page...)
		last := page[len(page)-1]
		curAt, curSeq, curID = last.EnqueuedAt, last.Sequence, last.NodeRunID
		if len(paged) > len(full) {
			t.Fatalf("paging: returned more rows than exist (duplicate rows)")
		}
	}
	assertSuffix(paged, full, "limit-2 paging")
}

// @concept: wait-set
// @concept: cascade-mode
// @decision: non-cascade-direct-to-stale
func testSelectCandidatesOrdersEqualTimestampRowsBySequence(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	q := d.Queue()
	fix := seedFixtureSet(ctx, t, d)

	at := time.Now().UTC().Add(-1 * time.Minute).Truncate(time.Second)
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
		for i := 0; i < 3; i++ {
			if err := q.Enqueue(ctx, persistence.DispatchRequest{
				NodeID:                 nodeID,
				ExecutorName:           "test-executor",
				RequiredClaimProducers: []string{},
				EnqueuedAt:             at,
				FrameID:                fix.FrameID,
				RunScopeID:             fix.MainRunScopeID,
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed three same-transaction rows: %v", err)
	}

	probeErr := errors.New("rollback probe")
	var got []persistence.Candidate
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{Limit: 100}, tx)
		if err != nil {
			return err
		}
		got = cands
		return probeErr
	})
	if err != nil && !errors.Is(err, probeErr) {
		t.Fatalf("SelectCandidates: %v", err)
	}

	var forNode []persistence.Candidate
	for _, c := range got {
		if c.NodeID == nodeID {
			forNode = append(forNode, c)
		}
	}
	if len(forNode) != 3 {
		t.Fatalf("got %d candidates for the node, want the 3 rows written in one transaction", len(forNode))
	}
	for i, c := range forNode {
		want := int64(i + 1)
		if c.Sequence != want {
			t.Fatalf("candidate %d carries sequence %d, want %d: rows written in one transaction share an enqueued_at, so creation order is the sequence order",
				i, c.Sequence, want)
		}
	}
}
