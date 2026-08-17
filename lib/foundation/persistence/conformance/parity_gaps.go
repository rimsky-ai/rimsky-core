// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

const paritySup = "parity-supervisor"

// @decision: parity-expansion
func testFrameEndFrameIfSettled(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()

	endIfSettled := func() persistence.FrameEndResult {
		t.Helper()
		var res persistence.FrameEndResult
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			res, err = frames.EndFrameIfSettled(ctx, fix.FrameID, tx)
			return err
		}); err != nil {
			t.Fatalf("EndFrameIfSettled: %v", err)
		}
		return res
	}

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, paritySup)
	if res := endIfSettled(); res.Transitioned {
		t.Fatalf("EndFrameIfSettled ended a frame holding an in-flight run: %+v", res)
	}

	completeRunAdmin(ctx, t, d, runID)
	res := endIfSettled()
	if !res.Transitioned {
		t.Fatalf("EndFrameIfSettled left the frame open after its only run retired")
	}
	if res.EndedAt == nil || res.StartedAt == nil {
		t.Fatalf("EndFrameIfSettled returned a transition without both timestamps: %+v", res)
	}
	if res.FinalState != persistence.FrameStateCompleted {
		t.Fatalf("EndFrameIfSettled final state = %q, want %q", res.FinalState, persistence.FrameStateCompleted)
	}

	if again := endIfSettled(); again.Transitioned {
		t.Fatalf("EndFrameIfSettled transitioned an already-ended frame: %+v", again)
	}
}

// @decision: parity-expansion
func testFrameListForObservability(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()

	list := func(filter persistence.FrameListFilter) persistence.PaginatedListResult[persistence.FrameRow] {
		t.Helper()
		var res persistence.PaginatedListResult[persistence.FrameRow]
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			res, err = frames.ListForObservability(ctx, filter, persistence.ListPagination{Limit: 50}, tx)
			return err
		}); err != nil {
			t.Fatalf("ListForObservability: %v", err)
		}
		return res
	}

	instanceID := fix.InstanceID
	got := list(persistence.FrameListFilter{InstanceID: &instanceID})
	if len(got.Rows) != 1 || got.Rows[0].FrameID != fix.FrameID {
		t.Fatalf("instance-filtered list = %+v, want exactly the fixture frame %s", got.Rows, fix.FrameID)
	}
	if got.Rows[0].TriggeringMessageID != fix.MessageID {
		t.Fatalf("row triggering message = %s, want %s", got.Rows[0].TriggeringMessageID, fix.MessageID)
	}
	if got.Rows[0].State != persistence.FrameStateRunning {
		t.Fatalf("row state = %q, want %q", got.Rows[0].State, persistence.FrameStateRunning)
	}

	otherInstance := shared.UUID(uuid.New())
	if empty := list(persistence.FrameListFilter{InstanceID: &otherInstance}); len(empty.Rows) != 0 {
		t.Fatalf("list leaked across instances: %+v", empty.Rows)
	}

	unresolved := false
	if ended := list(persistence.FrameListFilter{InstanceID: &instanceID, Unresolved: &unresolved}); len(ended.Rows) != 0 {
		t.Fatalf("ended-only filter surfaced a running frame: %+v", ended.Rows)
	}
}

// @decision: parity-expansion
// @concept: cascade-graph
func testFrameListForObservabilityWithMessage(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()

	instanceID := fix.InstanceID
	var res persistence.PaginatedListResult[persistence.FrameRowWithMessage]
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		res, err = frames.ListForObservabilityWithMessage(ctx,
			persistence.FrameListFilter{InstanceID: &instanceID},
			persistence.ListPagination{Limit: 50}, tx)
		return err
	}); err != nil {
		t.Fatalf("ListForObservabilityWithMessage: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0].FrameID != fix.FrameID {
		t.Fatalf("joined list = %+v, want exactly the fixture frame %s", res.Rows, fix.FrameID)
	}
	row := res.Rows[0]
	if row.MessageType != "fixture/message" || row.MessageSender != "operator" || row.MessageSenderKind != "operator" {
		t.Fatalf("joined row carries message %q/%q/%q, want fixture/message/operator/operator",
			row.MessageType, row.MessageSender, row.MessageSenderKind)
	}
	if row.TriggeringMessageID != fix.MessageID {
		t.Fatalf("joined row triggering message = %s, want %s", row.TriggeringMessageID, fix.MessageID)
	}
}

// @decision: parity-expansion
// @concept: cascade-graph
func testFrameGetForObservabilityWithMessage(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	frames := store.Frames()

	get := func(id shared.UUID) *persistence.FrameRowWithMessage {
		t.Helper()
		var row *persistence.FrameRowWithMessage
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			row, err = frames.GetForObservabilityWithMessage(ctx, id, tx)
			return err
		}); err != nil {
			t.Fatalf("GetForObservabilityWithMessage: %v", err)
		}
		return row
	}

	row := get(fix.FrameID)
	if row == nil {
		t.Fatalf("GetForObservabilityWithMessage returned nil for the fixture frame %s", fix.FrameID)
	}
	if row.MessageType != "fixture/message" || row.MessageSender != "operator" || row.MessageSenderKind != "operator" {
		t.Fatalf("joined row carries message %q/%q/%q, want fixture/message/operator/operator",
			row.MessageType, row.MessageSender, row.MessageSenderKind)
	}
	if row.InstanceID != fix.InstanceID {
		t.Fatalf("joined row instance = %s, want %s", row.InstanceID, fix.InstanceID)
	}

	if missing := get(shared.UUID(uuid.New())); missing != nil {
		t.Fatalf("GetForObservabilityWithMessage returned a row for an unknown frame: %+v", missing)
	}
}

// @decision: parity-expansion
func testQueueCountLive(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	instanceID := fix.InstanceID

	count := func(filter persistence.DispatchListFilter) int {
		t.Helper()
		filter.InstanceID = &instanceID
		n, err := q.CountLive(ctx, filter)
		if err != nil {
			t.Fatalf("CountLive: %v", err)
		}
		return n
	}

	if n := count(persistence.DispatchListFilter{}); n != 0 {
		t.Fatalf("CountLive on a fresh instance = %d, want 0", n)
	}

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	if n := count(persistence.DispatchListFilter{}); n != 1 {
		t.Fatalf("CountLive after one stale run = %d, want 1", n)
	}
	if n := count(persistence.DispatchListFilter{State: "pending"}); n != 1 {
		t.Fatalf("CountLive(pending) with an unclaimed run = %d, want 1", n)
	}
	if n := count(persistence.DispatchListFilter{State: "claimed"}); n != 0 {
		t.Fatalf("CountLive(claimed) with an unclaimed run = %d, want 0", n)
	}
	if n := count(persistence.DispatchListFilter{ExecutorName: "no-such-executor"}); n != 0 {
		t.Fatalf("CountLive filtered to a foreign executor = %d, want 0", n)
	}
	if n := count(persistence.DispatchListFilter{ExecutorName: "test-executor"}); n != 1 {
		t.Fatalf("CountLive filtered to the run's executor = %d, want 1", n)
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, runID, paritySup, tx)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow did not claim %s", runID)
		}
		_, err = q.PromoteClaimedToRunning(ctx, runID, paritySup, tx)
		return err
	}); err != nil {
		t.Fatalf("claim tx: %v", err)
	}
	if n := count(persistence.DispatchListFilter{State: "claimed"}); n != 1 {
		t.Fatalf("CountLive(claimed) after the claim = %d, want 1", n)
	}
	if n := count(persistence.DispatchListFilter{State: "pending"}); n != 0 {
		t.Fatalf("CountLive(pending) after the claim = %d, want 0", n)
	}

	completeRunAdmin(ctx, t, d, runID)
	if n := count(persistence.DispatchListFilter{}); n != 0 {
		t.Fatalf("CountLive counted a retired run = %d, want 0", n)
	}
}

// @decision: parity-expansion
func testQueueGetAnyByID(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()

	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, paritySup)
	row, err := q.GetAnyByID(ctx, runID)
	if err != nil {
		t.Fatalf("GetAnyByID: %v", err)
	}
	if row == nil {
		t.Fatalf("GetAnyByID returned nil for a claimed run %s", runID)
	}
	if row.NodeID != fix.NodeID || row.FrameID != fix.FrameID {
		t.Fatalf("GetAnyByID row = node %s frame %s, want node %s frame %s",
			row.NodeID, row.FrameID, fix.NodeID, fix.FrameID)
	}
	if row.ClaimedBy == nil || *row.ClaimedBy != paritySup {
		t.Fatalf("GetAnyByID row claimed_by = %v, want %q", row.ClaimedBy, paritySup)
	}

	completeRunAdmin(ctx, t, d, runID)
	retired, err := q.GetAnyByID(ctx, runID)
	if err != nil {
		t.Fatalf("GetAnyByID after retirement: %v", err)
	}
	if retired == nil {
		t.Fatalf("GetAnyByID hid a retired run: the any variant is state-blind by construction")
	}

	missing, err := q.GetAnyByID(ctx, shared.UUID(uuid.New()))
	if err != nil {
		t.Fatalf("GetAnyByID(unknown): %v", err)
	}
	if missing != nil {
		t.Fatalf("GetAnyByID returned a row for an unknown id: %+v", missing)
	}
}

func parityHandleInput(fix fixtureSet, nodeID shared.UUID, producer string) persistence.ClaimHandleInsertInput {
	p := producer
	intent := "rw"
	frame := fix.FrameID
	return persistence.ClaimHandleInsertInput{
		ID:                 uuid.New(),
		LockKind:           persistence.LockKindScope,
		ProducerName:       &p,
		ClaimScopeData:     json.RawMessage(`{"path":"/parity/` + producer + `"}`),
		Intent:             &intent,
		HolderSupervisorID: paritySup,
		HolderNodeID:       nodeID,
		FrameID:            &frame,
		ExpiresAt:          time.Now().Add(1 * time.Hour),
	}
}

// @decision: parity-expansion
func testClaimHandleGetByFrameAndNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	in := parityHandleInput(fix, fix.NodeID, "parity-producer-a")
	seedGuardClaimHandle(ctx, t, d, in)

	get := func(nodeID, frameID shared.UUID) *persistence.ClaimHandleRow {
		t.Helper()
		var row *persistence.ClaimHandleRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			row, err = store.ClaimHandles().GetByFrameAndNode(ctx, nodeID, frameID, tx)
			return err
		}); err != nil {
			t.Fatalf("GetByFrameAndNode: %v", err)
		}
		return row
	}

	row := get(fix.NodeID, fix.FrameID)
	if row == nil || row.ID != in.ID {
		t.Fatalf("GetByFrameAndNode = %+v, want the seeded handle %s", row, in.ID)
	}

	if other := get(shared.UUID(uuid.New()), fix.FrameID); other != nil {
		t.Fatalf("GetByFrameAndNode matched a foreign node: %+v", other)
	}
	if other := get(fix.NodeID, shared.UUID(uuid.New())); other != nil {
		t.Fatalf("GetByFrameAndNode matched a foreign frame: %+v", other)
	}
}

// @decision: parity-expansion
func testClaimHandleListForObservability(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	nodeB := seedExtraNode(ctx, t, d, fix, "parity-observability-node-b")
	inA := parityHandleInput(fix, fix.NodeID, "parity-producer-a")
	inB := parityHandleInput(fix, nodeB, "parity-producer-b")
	seedGuardClaimHandle(ctx, t, d, inA)
	seedGuardClaimHandle(ctx, t, d, inB)

	list := func(filter persistence.ClaimHandleListFilter, limit int) persistence.PaginatedListResult[persistence.ClaimHandleRow] {
		t.Helper()
		var res persistence.PaginatedListResult[persistence.ClaimHandleRow]
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			res, err = store.ClaimHandles().ListForObservability(ctx, filter,
				persistence.ListPagination{Limit: limit}, tx)
			return err
		}); err != nil {
			t.Fatalf("ListForObservability: %v", err)
		}
		return res
	}

	instanceID := fix.InstanceID
	all := list(persistence.ClaimHandleListFilter{InstanceID: &instanceID}, 50)
	if len(all.Rows) != 2 {
		t.Fatalf("instance-filtered list = %d rows, want the 2 seeded handles", len(all.Rows))
	}

	byProducer := list(persistence.ClaimHandleListFilter{ProducerName: "parity-producer-b"}, 50)
	if len(byProducer.Rows) != 1 || byProducer.Rows[0].ID != inB.ID {
		t.Fatalf("producer-filtered list = %+v, want exactly handle %s", byProducer.Rows, inB.ID)
	}

	byNode := list(persistence.ClaimHandleListFilter{HolderNodeID: &nodeB}, 50)
	if len(byNode.Rows) != 1 || byNode.Rows[0].ID != inB.ID {
		t.Fatalf("node-filtered list = %+v, want exactly handle %s", byNode.Rows, inB.ID)
	}

	page := list(persistence.ClaimHandleListFilter{InstanceID: &instanceID}, 1)
	if len(page.Rows) != 1 {
		t.Fatalf("limit-1 page = %d rows, want 1", len(page.Rows))
	}
	if page.NextCursor == "" {
		t.Fatalf("limit-1 page over 2 rows returned no next cursor")
	}
}

// @decision: parity-expansion
func testClaimHandleDeleteResolvedIfNoActiveHolders(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	deleteResolved := func(id shared.UUID) bool {
		t.Helper()
		var deleted bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			deleted, err = store.ClaimHandles().DeleteResolvedIfNoActiveHolders(ctx, id, tx)
			return err
		}); err != nil {
			t.Fatalf("DeleteResolvedIfNoActiveHolders: %v", err)
		}
		return deleted
	}
	promote := func(id shared.UUID) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.ClaimHandles().Promote(ctx, id, paritySup, spec.ClaimHandleStateCommitted, tx)
		}); err != nil {
			t.Fatalf("Promote: %v", err)
		}
	}

	active := parityHandleInput(fix, fix.NodeID, "parity-producer-active")
	seedGuardClaimHandle(ctx, t, d, active)
	if deleteResolved(active.ID) {
		t.Fatalf("DeleteResolvedIfNoActiveHolders deleted an active handle")
	}

	promote(active.ID)
	if !deleteResolved(active.ID) {
		t.Fatalf("DeleteResolvedIfNoActiveHolders left a resolved holder-free handle in place")
	}
	if row := getGuardClaimHandle(ctx, t, d, active.ID); row != nil {
		t.Fatalf("resolved handle survived the delete: %+v", row)
	}

	nodeB := seedExtraNode(ctx, t, d, fix, "parity-resolved-node-b")
	held := parityHandleInput(fix, nodeB, "parity-producer-held")
	seedGuardClaimHandle(ctx, t, d, held)
	runID := seedConformanceRunForNode(ctx, t, d, nodeB, fix.FrameID)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:              shared.UUID(uuid.New()),
			ClaimHandleID:   held.ID,
			HolderNodeRunID: runID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed active holder: %v", err)
	}
	promote(held.ID)
	if deleteResolved(held.ID) {
		t.Fatalf("DeleteResolvedIfNoActiveHolders deleted a handle still carrying an active holder")
	}
}
