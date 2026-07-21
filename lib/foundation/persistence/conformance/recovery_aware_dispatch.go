// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: run-scope
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testRecoveryAwareDispatch(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	parentNodeRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	partitionScopeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               partitionScopeID,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentNodeRunID:  &parentNodeRunID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "part-recovery",
		})
	}); err != nil {
		t.Fatalf("Create partition scope: %v", err)
	}

	childNodeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         childNodeID,
			InstanceID: fix.InstanceID,
			NodeType:   "fixture-node-type",
			Executor:   "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("Create child node: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 childNodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-2 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             partitionScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx (original): %v", err)
	}

	var originalNodeRunID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == childNodeID {
				originalNodeRunID = c.NodeRunID
				return nil
			}
		}
		t.Fatalf("original dispatch not surfaced by SelectCandidates")
		return nil
	}); err != nil {
		t.Fatalf("SelectCandidates (original): %v", err)
	}

	scratchFixture := []byte("scratch-bytes-fixture")

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, originalNodeRunID, "sup-stale")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow(original) did not claim")
		}
		if err := q.WriteScratchInTx(ctx, tx, originalNodeRunID, scratchFixture, "", ""); err != nil {
			return err
		}
		if err := store.Nodes().UpdateState(ctx, originalNodeRunID,
			cascade.NodeStateFailed, cascade.ReasonInstanceKilled, nil, tx); err != nil {
			return err
		}
		return q.RemoveForNodeInTx(ctx, childNodeID, partitionScopeID, "sup-stale", tx)
	}); err != nil {
		t.Fatalf("Remove original: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		scratchInline, scratchHandle, scratchBackend, lerr := q.LoadScratchInTx(ctx, tx, originalNodeRunID)
		if lerr != nil {
			return lerr
		}
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                      childNodeID,
			ExecutorName:                "test-executor",
			RequiredClaimProducers:      []string{},
			EnqueuedAt:                  time.Now().Add(-time.Second),
			FrameID:                     fix.FrameID,
			RunScopeID:                  partitionScopeID,
			PriorNodeRunID:              &originalNodeRunID,
			PriorDispatchDisposition:    "stale_recovery",
			InitialScratchInline:        scratchInline,
			InitialScratchHandle:        scratchHandle,
			InitialScratchHandleBackend: scratchBackend,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx (recovery): %v", err)
	}

	var got persistence.Candidate
	var found bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == childNodeID {
				got = c
				found = true
				return nil
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SelectCandidates (recovery): %v", err)
	}
	if !found {
		t.Fatalf("recovery dispatch not surfaced by SelectCandidates")
	}
	if got.PriorNodeRunID == nil {
		t.Fatalf("Candidate.PriorNodeRunID = nil; want %v", originalNodeRunID)
	}
	if *got.PriorNodeRunID != originalNodeRunID {
		t.Fatalf("Candidate.PriorNodeRunID = %v; want %v", *got.PriorNodeRunID, originalNodeRunID)
	}
	if got.PriorDispatchDisposition != "stale_recovery" {
		t.Fatalf("Candidate.PriorDispatchDisposition = %q; want stale_recovery", got.PriorDispatchDisposition)
	}

	var gotInline []byte
	var gotHandle, gotBackend string
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var lerr error
		gotInline, gotHandle, gotBackend, lerr = q.LoadScratchInTx(ctx, tx, got.NodeRunID)
		return lerr
	}); err != nil {
		t.Fatalf("LoadScratchInTx (recovery): %v", err)
	}
	if string(gotInline) != string(scratchFixture) {
		t.Fatalf("recovery scratch_inline = %q; want %q", string(gotInline), string(scratchFixture))
	}
	if gotHandle != "" {
		t.Fatalf("recovery scratch_handle = %q; want empty", gotHandle)
	}
	if gotBackend != "" {
		t.Fatalf("recovery scratch_handle_backend = %q; want empty", gotBackend)
	}
}

func testRecoveryDispositionStamps(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, runID, "sup-owner")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow did not claim")
		}
		return nil
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := q.ReleaseClaimWithDisposition(ctx, runID, "sup-wrong", "stale_recovery"); err != nil {
		t.Fatalf("ReleaseClaimWithDisposition(wrong claimant): %v", err)
	}
	own, err := q.GetClaimedBy(ctx, runID)
	if err != nil {
		t.Fatalf("GetClaimedBy: %v", err)
	}
	if own.Kind != persistence.ClaimOwnershipKindClaimedBy || own.SupervisorID != "sup-owner" {
		t.Fatalf("wrong-claimant release must be a no-op; ownership = %+v", own)
	}

	if err := q.ReleaseClaimWithDisposition(ctx, runID, "sup-owner", "stale_recovery"); err != nil {
		t.Fatalf("ReleaseClaimWithDisposition: %v", err)
	}
	assertCandidateDisposition(ctx, t, store, q, fix.NodeID, runID, runID, "stale_recovery")

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.StampPriorDispatchInTx(ctx, tx, runID, runID, "retry_after_error")
	}); err != nil {
		t.Fatalf("StampPriorDispatchInTx(retry_after_error): %v", err)
	}
	assertCandidateDisposition(ctx, t, store, q, fix.NodeID, runID, runID, "retry_after_error")

	missingErr := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.StampPriorDispatchInTx(ctx, tx, shared.UUID(uuid.New()), runID, "retry_after_error")
	})
	if !errors.Is(missingErr, persistence.ErrRunRowMissing) {
		t.Fatalf("StampPriorDispatchInTx(missing row): want ErrRunRowMissing, got %v", missingErr)
	}
}

func assertCandidateDisposition(
	ctx context.Context, t *testing.T, store persistence.Tables, q persistence.Queue,
	nodeID, runID, wantPriorID shared.UUID, wantDisposition string,
) {
	t.Helper()
	var got *persistence.Candidate
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor", "stub"},
			AcceptedClaimProducers: []string{},
			Limit:                  32,
		})
		if err != nil {
			return err
		}
		for i := range cands {
			if cands[i].NodeRunID == runID {
				got = &cands[i]
				return nil
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SelectCandidates: %v", err)
	}
	if got == nil {
		t.Fatalf("run %s for node %s not surfaced by SelectCandidates", runID, nodeID)
	}
	if got.PriorNodeRunID == nil || *got.PriorNodeRunID != wantPriorID {
		t.Fatalf("Candidate.PriorNodeRunID = %v; want %v", got.PriorNodeRunID, wantPriorID)
	}
	if got.PriorDispatchDisposition != wantDisposition {
		t.Fatalf("Candidate.PriorDispatchDisposition = %q; want %q", got.PriorDispatchDisposition, wantDisposition)
	}
}

func testSelectCandidatesSkipsUndrainedWaitSet(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	senderRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	receiverNodeID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         receiverNodeID,
			InstanceID: fix.InstanceID,
			NodeType:   "fixture-receiver-type",
			Executor:   "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("Create receiver node: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 receiverNodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx (receiver): %v", err)
	}

	receiverRunID := findCandidateRun(ctx, t, store, q, receiverNodeID)
	if receiverRunID == (shared.UUID{}) {
		t.Fatalf("receiver run not surfaced before wait-set row insert")
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID:           fix.FrameID,
			ReceiverNodeRunID: receiverRunID,
			SenderNodeRunID:   senderRunID,
			TopicKind:         "terminal",
		}, tx)
	}); err != nil {
		t.Fatalf("WaitSet.Insert: %v", err)
	}
	if got := findCandidateRun(ctx, t, store, q, receiverNodeID); got != (shared.UUID{}) {
		t.Fatalf("SelectCandidates surfaced run %s with an undrained wait-set row; the queue predicate must gate on drained_at", got)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fix.FrameID, senderRunID, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender: %v", err)
	}
	if got := findCandidateRun(ctx, t, store, q, receiverNodeID); got != receiverRunID {
		t.Fatalf("SelectCandidates after drain = %v; want %v", got, receiverRunID)
	}
}

func findCandidateRun(
	ctx context.Context, t *testing.T, store persistence.Tables, q persistence.Queue, nodeID shared.UUID,
) shared.UUID {
	t.Helper()
	var out shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor", "stub"},
			AcceptedClaimProducers: []string{},
			Limit:                  32,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				out = c.NodeRunID
				return nil
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("SelectCandidates: %v", err)
	}
	return out
}

func testScratchMissingRowContract(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	q := d.Queue()

	missingID := shared.UUID(uuid.New())

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		inline, handle, backend, lerr := q.LoadScratchInTx(ctx, tx, missingID)
		if lerr != nil {
			t.Fatalf("LoadScratchInTx (missing): unexpected error %v", lerr)
		}
		if len(inline) != 0 {
			t.Fatalf("LoadScratchInTx (missing): inline = %q; want empty", string(inline))
		}
		if handle != "" {
			t.Fatalf("LoadScratchInTx (missing): handle = %q; want empty", handle)
		}
		if backend != "" {
			t.Fatalf("LoadScratchInTx (missing): backend = %q; want empty", backend)
		}
		return nil
	}); err != nil {
		t.Fatalf("LoadScratchInTx (missing): tx failure %v", err)
	}

	werr := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.WriteScratchInTx(ctx, tx, missingID, []byte("bytes"), "", "")
	})
	if werr == nil {
		t.Fatalf("WriteScratchInTx (missing): want ErrRunRowMissing, got nil")
	}
	if !errors.Is(werr, persistence.ErrRunRowMissing) {
		t.Fatalf("WriteScratchInTx (missing): want ErrRunRowMissing, got %v", werr)
	}
}
