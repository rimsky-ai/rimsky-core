// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// runner_subclaim.go — E4 atomic-acquisition extension for fan-out
// nodes. Spec: .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Fan-out template DSL + §Recursive claim-tree resolution.
//
// @concept: claim-tree
// @concept: fan-out
//
// When a template node declares `fan_out:`, the acquisition transaction
// grows to call `ClaimProducer.SplitScope` on the parent claim handle
// (already Open'd via the standard acquireClaim path). The producer
// returns a list of `SubClaimScopeDescriptor`s; rimsky INSERTs one
// rimsky_claim_handles row per sub-claim-scope with `parent_claim_handle_id`
// pointing at the parent. Each sub-claim is Open'd against the producer
// in the same transaction so atomicity discipline holds
// (`@blessed-invariant 10`).
//
// Producer-side `data_processing` capability advertisement is consulted
// per sub-claim: when advertised, rimsky stores the
// `producer_candidate_handle` returned by `BeginCandidate` so the
// leaf-dispatch path can populate `ExecuteRequest.StoreHandle.candidate_handle`
// (spec §A5; runtime/runner_dispatch.go).

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// AcquireSubClaimsInput bundles the parent-claim row + the canonicalized
// fan-out spec the caller wants to split across.
type AcquireSubClaimsInput struct {
	ParentClaimHandleID shared.UUID
	ParentClaimScope    json.RawMessage
	ProducerName        string
	NodeRunID           shared.UUID
	HolderNodeID        shared.UUID
	HolderSupervisorID  string
	FrameID             *shared.UUID
	HeartbeatInterval   time.Duration
	// PartitionRequest is the producer-interpreted bytes that drive
	// SplitScope. Caller is responsible for substitution; rimsky passes
	// the bytes verbatim per `@blessed-invariant 20` (claim content is
	// inert).
	PartitionRequest []byte
	// Lifetime carries the parent claim's lifetime hint; sub-claims
	// inherit "subgraph" unless the parent declared "durable".
	//
	// @concept: claim-lifetime
	Lifetime spec.ClaimLifetime
	// ParentIsHeld carries the parent claim_handle's `is_held` value.
	// Sub-claims inherit it so the row persists past the fan-out leaf's
	// own active-terminal until the parent's recursive resolution
	// (`auto_terminal.go::resolveParentClaimChain`) walks
	// `ListChildClaimHandles` and finds them. Without inheritance the
	// non-held sub-claim row drops at active-terminal of the leaf run
	// and the parent's aggregation sees an empty children set,
	// Committing prematurely. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Recursive claim-tree resolution.
	ParentIsHeld bool
	// AggregationPolicy is the fan-out parent's error policy snapshotted
	// from the template-node spec. Persisted on the parent claim_handle
	// row at the first sub-claim acquisition so the recursive walker
	// (`runtime/auto_terminal.go::resolveParentClaimChain`) can compute a
	// true aggregate Commit/Abandon decision over ALL children's
	// outcomes — not just the just-resolved seedOutcome (cycle 4 issue C).
	// Empty policy → recursive walker defaults to `strict` semantics.
	AggregationPolicy spec.AggregationPolicy
}

// SubClaim is one acquired sub-claim row. The caller wires these into
// per-leaf dispatch (parent run's children).
type SubClaim struct {
	ClaimHandleID           shared.UUID
	PartitionKey            string
	Address                 json.RawMessage
	ClaimScope              json.RawMessage
	ProducerCandidateHandle []byte
}

// AcquireSubClaims is the E4 hot path. Given an already-acquired parent
// claim handle, call SplitScope on the producer, then for each
// sub-scope returned: INSERT a sub-claim row with
// `parent_claim_handle_id = parent` and the producer-canonicalized
// `claim_scope_data`. When the producer advertises `data_processing`, also
// call `BeginCandidate` and persist the returned `candidate_handle`;
// otherwise the candidate handle slot stays empty (the leaf executor
// can still operate against the parent's address + the sub-claim's
// claim_scope_data).
//
// Per-sub-claim Open is NOT issued: the SplitScope response IS the
// per-sub-claim acquisition (the producer already partitioned the
// parent's scope), and the proto's `OpenRequest.selector` field is a
// `string` that cannot losslessly carry arbitrary `claim_scope_data` bytes.
// Re-issuing Open with `string(desc.ClaimScopeData)` as the selector
// double-encoded the canonicalized scope into a substitution-time
// selector form the producer's parser does not expect (the
// scope-data canonical form is producer-internal; the selector is the
// operator-supplied template form). Sub-claim disposition flows
// through CommitCandidate / AbandonCandidate on the DataProcessing
// surface and through the parent's auto-terminal verb selection on the
// standard ClaimProducer surface.
//
// Returns one SubClaim per descriptor. The slice ordering matches the
// producer's SubClaimScopes ordering — caller may sort by `partition_key` if
// the dispatcher needs deterministic ordering for child-run idempotency.
//
// Atomicity per `@blessed-invariant 10`: every sub-claim INSERT + every
// per-sub-claim BeginCandidate happens inside the caller's tx. Failure
// on any sub-claim aborts the tx, rolling back the entire fan-out
// acquisition.
func AcquireSubClaims(
	ctx context.Context, args RunArgs, tx persistence.Tx, in AcquireSubClaimsInput,
) ([]SubClaim, error) {
	producer, ok := args.StoreRegistry.Get(in.ProducerName)
	if !ok {
		return nil, fmt.Errorf("AcquireSubClaims: unknown producer %q", in.ProducerName)
	}
	// SplitScope is optional — caller should gate on the producer's
	// advertised capability. The RPC client returns
	// claimproducer.ErrSplitScopeUnsupported when the producer doesn't
	// advertise. Surface as a typed error so callers can route to the
	// validation pipeline (D4).
	resp, err := producer.SplitScope(ctx, locks.SplitClaimScopeRequest{
		ClaimHandleID:    in.ParentClaimHandleID.String(),
		PartitionRequest: in.PartitionRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("AcquireSubClaims: SplitScope(%s): %w", in.ProducerName, err)
	}
	// Resolve the optional DataProcessing client; absence is fine — the
	// candidate handle slot stays empty and the leaf executor falls
	// back to claim_scope_data + parent address alone.
	var dpClient DataProcessingClient
	if args.DataProcessors != nil {
		if c, ok := args.DataProcessors.Get(in.ProducerName); ok {
			dpClient = c
		}
	}
	out := make([]SubClaim, 0, len(resp.SubClaimScopes))
	parentID := in.ParentClaimHandleID
	lifetime := in.Lifetime
	if lifetime == "" {
		lifetime = spec.ClaimLifetimeSubgraph
	}
	// Persist the parent's aggregation policy snapshot ONCE per fan-out
	// acquisition. The recursive walker reads it at parent resolution
	// time to compute the true aggregate outcome over all children
	// (cycle 4 issue C). Empty policy → leave column NULL; the walker
	// defaults to strict semantics. Claimant-guarded via parent's
	// supervisor id (which equals the caller's HolderSupervisorID by
	// construction — the same supervisor is acquiring the sub-claims
	// against the parent it already holds).
	if in.AggregationPolicy.Kind != "" {
		policyBytes, mErr := persistence.MarshalAggregationPolicy(in.AggregationPolicy)
		if mErr != nil {
			return nil, fmt.Errorf("AcquireSubClaims: marshal aggregation_policy: %w", mErr)
		}
		if len(policyBytes) > 0 {
			if err := args.ClaimHandles.SetAggregationPolicy(ctx, parentID, in.HolderSupervisorID, policyBytes, tx); err != nil {
				return nil, fmt.Errorf("AcquireSubClaims: SetAggregationPolicy on parent: %w", err)
			}
		}
	}
	for _, desc := range resp.SubClaimScopes {
		subID := shared.UUID(uuid.New())
		// BeginCandidate runs BEFORE the row INSERT so the
		// `producer_candidate_handle` column carries the producer's
		// handle bytes from the start. The verb is claim-id-keyed for
		// idempotency per spec §Protocol surfaces / DataProcessing.
		// Failure aborts the sub-claim acquisition; the parent tx
		// rollback removes any sibling sub-claim rows already INSERTed.
		var candidateHandle []byte
		if dpClient != nil {
			beginOut, beginErr := dpClient.BeginCandidate(ctx, BeginCandidateInput{
				ProducerName:       in.ProducerName,
				ClaimHandleID:      subID.String(),
				SubScopeDescriptor: desc.ClaimScopeData,
				IdempotencyKey:     subID.String(),
			})
			if beginErr != nil {
				return nil, fmt.Errorf("AcquireSubClaims: BeginCandidate(%s): %w", in.ProducerName, beginErr)
			}
			candidateHandle = beginOut.CandidateHandle
			// Forensics: emit `subclaim.begin_candidate` per accepted
			// candidate. Payload carries rimsky-side identifiers + the
			// candidate-handle SIZE (not the bytes — @blessed-invariant
			// 20 keeps candidate content inert in rimsky). Best-effort:
			// a logging failure is logged, not propagated, so the
			// acquisition tx still commits the sub-claim row.
			emitSubclaimBeginCandidate(ctx, args, tx, parentID, subID, in.ProducerName, len(candidateHandle))
		}
		// Persist the sub-claim row. The canonical claim_scope_data flows
		// verbatim onto the row (inert per @blessed-invariant 20). No
		// per-sub-claim Open RPC fires — SplitScope's response IS the
		// per-sub-claim acquisition; address bytes default to empty and
		// the leaf executor reads claim_scope_data + candidate_handle when
		// dispatching.
		intent := "rw"
		insert := persistence.ClaimHandleInsertInput{
			ID:                  subID,
			NodeRunID:           &in.NodeRunID,
			LockKind:            persistence.LockKindScope,
			ProducerName:        &in.ProducerName,
			ClaimScopeData:      json.RawMessage(desc.ClaimScopeData),
			Intent:              &intent,
			HolderSupervisorID:  in.HolderSupervisorID,
			HolderNodeID:        in.HolderNodeID,
			ExpiresAt:           args.Clock.Now().Add(5 * in.HeartbeatInterval),
			FrameID:             in.FrameID,
			ParentClaimHandleID: &parentID,
			Lifetime:            lifetime,
			// Sub-claims inherit the parent's is_held value so the row
			// persists past the fan-out leaf's active terminal until
			// `resolveParentClaimChain` walks the children at parent
			// resolution time. See `AcquireSubClaimsInput.ParentIsHeld`.
			IsHeld:                  in.ParentIsHeld,
			ProducerCandidateHandle: candidateHandle,
		}
		if err := args.ClaimHandles.Insert(ctx, insert, tx); err != nil {
			return nil, fmt.Errorf("AcquireSubClaims: Insert sub-claim: %w", err)
		}
		// Bump the parent's expected_children_count so the recursive
		// walker can detect "all children resolved" via
		// committed+abandoned == expected (cycle 4 issue C). Claimant-
		// guarded on the same supervisor that holds the parent.
		if err := args.ClaimHandles.BumpExpectedChildrenCount(ctx, parentID, in.HolderSupervisorID, 1, tx); err != nil {
			return nil, fmt.Errorf("AcquireSubClaims: BumpExpectedChildrenCount on parent: %w", err)
		}
		out = append(out, SubClaim{
			ClaimHandleID:           subID,
			PartitionKey:            desc.PartitionKey,
			Address:                 nil,
			ClaimScope:              json.RawMessage(desc.ClaimScopeData),
			ProducerCandidateHandle: candidateHandle,
		})
	}
	// Forensics: emit a single `subclaim.acquired` event summarizing the
	// full split. Captures the count rather than the per-sub-claim
	// scope-data bytes so the event log stays inert per @blessed-
	// invariant 20.
	emitSubclaimAcquired(ctx, args, tx, parentID, in.HolderNodeID, in.ProducerName, len(out))
	return out, nil
}

// emitSubclaimBeginCandidate writes one rimsky_events row per accepted
// candidate. Best-effort: a logging failure is logged via args.Logger
// and not propagated, so the acquisition tx still commits the row.
//
// Honors @blessed-invariant 20 (claim content inert): only the
// candidate-handle SIZE is recorded; the bytes themselves are inert in
// rimsky.
func emitSubclaimBeginCandidate(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	parentID, subID shared.UUID, producerName string, candidateHandleSize int,
) {
	if args.Persist == nil {
		return
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		Kind: "subclaim.begin_candidate",
		Payload: map[string]any{
			"parent_claim_handle_id":      parentID.String(),
			"sub_claim_handle_id":         subID.String(),
			"producer_name":               producerName,
			"candidate_handle_size_bytes": candidateHandleSize,
		},
	}, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("AcquireSubClaims: event append failed",
			"kind", "subclaim.begin_candidate",
			"sub_claim_handle_id", subID.String(),
			"error", err.Error())
	}
}

// emitSubclaimAcquired writes the per-acquisition summary event after
// every sub-claim row has been INSERTed. The descriptor count is the
// fan-out width; the producer_name lets observability filter to a
// specific store. Same best-effort posture as emitSubclaimBeginCandidate.
func emitSubclaimAcquired(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	parentID, holderNodeID shared.UUID, producerName string, subScopeCount int,
) {
	if args.Persist == nil || subScopeCount == 0 {
		return
	}
	nodeID := holderNodeID
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &nodeID,
		Kind:   "subclaim.acquired",
		Payload: map[string]any{
			"parent_claim_handle_id":     parentID.String(),
			"sub_scope_descriptor_count": subScopeCount,
			"producer_name":              producerName,
		},
	}, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("AcquireSubClaims: event append failed",
			"kind", "subclaim.acquired",
			"parent_claim_handle_id", parentID.String(),
			"error", err.Error())
	}
}
