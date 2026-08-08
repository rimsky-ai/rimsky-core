// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: claim-tree
// @concept: fan-out

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type AcquireSubClaimsInput struct {
	ParentClaimHandleID shared.UUID
	ProducerName        string
	NodeRunID           shared.UUID
	HolderNodeID        shared.UUID
	HolderSupervisorID  string
	InstanceID          shared.UUID
	FrameID             *shared.UUID
	LivenessInterval    time.Duration
	PartitionRequest    []byte
	// @concept: claim-lifetime
	Lifetime                     spec.ClaimLifetime
	ParentIsHeld                 bool
	AggregationPolicy            spec.AggregationPolicy
	ParentIntent                 string
	ParentRealizedWriteSemantics string
}

type SubClaim struct {
	ClaimHandleID           shared.UUID
	PartitionKey            string
	ClaimScope              json.RawMessage
	ProducerCandidateHandle []byte
	ProducerLeaseToken      string
}

type begunSubClaimCandidate struct {
	claimHandleID   string
	candidateHandle []byte
}

func subClaimIdempotencyKey(nodeRunID shared.UUID, partitionKey string) string {
	return fmt.Sprintf("subclaim:%s:%s", nodeRunID.String(), partitionKey)
}

func AcquireSubClaims(
	ctx context.Context, args RunArgs, in AcquireSubClaimsInput, tx persistence.Tx,
) (result []SubClaim, retErr error) {
	if in.ParentIntent == "" {
		return nil, fmt.Errorf("AcquireSubClaims: ParentIntent must be set (got empty); "+
			"caller must thread the parent claim's intent (producer=%q)", in.ProducerName)
	}
	producer, ok, resolveErr := args.ClaimProducerRegistry.ResolveWithContext(ctx, in.ProducerName, in.InstanceID.String(), tx)
	if resolveErr != nil {
		return nil, fmt.Errorf("AcquireSubClaims: resolving producer %q: %w", in.ProducerName, resolveErr)
	}
	if !ok {
		return nil, fmt.Errorf("AcquireSubClaims: unknown producer %q", in.ProducerName)
	}
	resp, err := producer.SplitScope(ctx, claimproducer.SplitClaimScopeRequest{
		ClaimHandleID:    in.ParentClaimHandleID.String(),
		PartitionRequest: in.PartitionRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("AcquireSubClaims: SplitScope(%s): %w", in.ProducerName, err)
	}
	var dpClient DataProcessingClient
	if args.DataProcessors != nil {
		if c, ok := args.DataProcessors.Get(in.ProducerName); ok {
			dpClient = c
		}
	}
	var begunCandidates []begunSubClaimCandidate
	defer func() {
		if retErr == nil || dpClient == nil {
			return
		}
		for _, b := range begunCandidates {
			if err := dpClient.AbandonCandidate(ctx, AbandonCandidateInput{
				ProducerName:    in.ProducerName,
				ClaimHandleID:   b.claimHandleID,
				CandidateHandle: b.candidateHandle,
			}); err != nil && args.Logger != nil {
				args.Logger.Warn("AcquireSubClaims: AbandonCandidate on unwind failed",
					"producer", in.ProducerName,
					"sub_claim_handle_id", b.claimHandleID,
					"error", err.Error())
			}
		}
	}()
	caps, err := producer.Capabilities(ctx)
	if err != nil {
		return nil, fmt.Errorf("AcquireSubClaims: Capabilities(%s): %w", in.ProducerName, err)
	}
	out := make([]SubClaim, 0, len(resp.SubClaimScopes))
	parentID := in.ParentClaimHandleID
	lifetime := in.Lifetime
	if lifetime == "" {
		lifetime = spec.ClaimLifetimeSubgraph
	}
	acceptedScopes := make([][]byte, 0, len(resp.SubClaimScopes))
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
		if desc.PartitionKey == "" {
			return nil, fmt.Errorf("AcquireSubClaims: producer %q returned a sub-claim scope with an empty partition_key; "+
				"every SplitScope descriptor must carry a non-empty partition_key (the empty key is reserved for the delegation shape)",
				in.ProducerName)
		}
		if len(desc.Address) > 0 && !json.Valid(desc.Address) {
			return nil, fmt.Errorf("AcquireSubClaims: producer %q returned a sub-claim scope (partition_key=%q) "+
				"with non-JSON address bytes; the persistence layer requires JSON-valid address bytes "+
				"(producers must JSON-encode opaque bytes — wrap in a base64-tagged JSON envelope if non-JSON)",
				in.ProducerName, desc.PartitionKey)
		}
		if len(desc.Payload) > 0 && !json.Valid(desc.Payload) {
			return nil, fmt.Errorf("AcquireSubClaims: producer %q returned a sub-claim scope (partition_key=%q) "+
				"with non-JSON payload bytes; the persistence layer requires JSON-valid payload bytes "+
				"(producers must JSON-encode opaque bytes — wrap in a base64-tagged JSON envelope if non-JSON)",
				in.ProducerName, desc.PartitionKey)
		}
		subScope := json.RawMessage(desc.ClaimScopeData)
		for _, prior := range acceptedScopes {
			conflicts, cErr := scopesConflict(ctx, producer, caps, subScope, prior)
			if cErr != nil {
				return nil, fmt.Errorf("AcquireSubClaims: ScopesConflict(%s): %w", in.ProducerName, cErr)
			}
			if conflicts {
				return nil, fmt.Errorf("AcquireSubClaims: overlapping sub-claim scopes for producer %q "+
					"(partition_key %q conflicts with an already-acquired sibling) — rejecting the whole "+
					"fan-out acquisition; sibling sub-claim scopes must not overlap", in.ProducerName, desc.PartitionKey)
			}
		}
		subID := shared.UUID(uuid.New())
		var candidateHandle []byte
		if dpClient != nil {
			beginOut, beginErr := dpClient.BeginCandidate(ctx, BeginCandidateInput{
				ProducerName:       in.ProducerName,
				ClaimHandleID:      subID.String(),
				SubScopeDescriptor: desc.ClaimScopeData,
				IdempotencyKey:     subClaimIdempotencyKey(in.NodeRunID, desc.PartitionKey),
			})
			if beginErr != nil {
				return nil, fmt.Errorf("AcquireSubClaims: BeginCandidate(%s): %w", in.ProducerName, beginErr)
			}
			candidateHandle = beginOut.CandidateHandle
			begunCandidates = append(begunCandidates, begunSubClaimCandidate{
				claimHandleID:   subID.String(),
				candidateHandle: candidateHandle,
			})
			emitSubclaimBeginCandidate(ctx, args, parentID, subID, in.InstanceID, in.HolderNodeID, in.ProducerName, len(candidateHandle), tx)
		}
		intent := in.ParentIntent
		insert := persistence.ClaimHandleInsertInput{
			ID:                      subID,
			NodeRunID:               &in.NodeRunID,
			LockKind:                persistence.LockKindScope,
			ProducerName:            &in.ProducerName,
			ClaimScopeData:          json.RawMessage(desc.ClaimScopeData),
			Address:                 json.RawMessage(desc.Address),
			Payload:                 json.RawMessage(desc.Payload),
			Intent:                  &intent,
			HolderSupervisorID:      in.HolderSupervisorID,
			HolderNodeID:            in.HolderNodeID,
			ExpiresAt:               claimExpiryFromLiveness(args.Clock.Now(), in.LivenessInterval),
			FrameID:                 in.FrameID,
			ParentClaimHandleID:     &parentID,
			Lifetime:                lifetime,
			IsHeld:                  in.ParentIsHeld,
			RealizedWriteSemantics:  in.ParentRealizedWriteSemantics,
			ProducerCandidateHandle: candidateHandle,
			ProducerLeaseToken:      desc.LeaseToken,
		}
		if err := args.ClaimHandles.Insert(ctx, insert, tx); err != nil {
			return nil, fmt.Errorf("AcquireSubClaims: Insert sub-claim: %w", err)
		}
		if err := args.ClaimHandles.BumpExpectedChildrenCount(ctx, parentID, in.HolderSupervisorID, 1, tx); err != nil {
			return nil, fmt.Errorf("AcquireSubClaims: BumpExpectedChildrenCount on parent: %w", err)
		}
		acceptedScopes = append(acceptedScopes, desc.ClaimScopeData)
		out = append(out, SubClaim{
			ClaimHandleID:           subID,
			PartitionKey:            desc.PartitionKey,
			ClaimScope:              json.RawMessage(desc.ClaimScopeData),
			ProducerCandidateHandle: candidateHandle,
			ProducerLeaseToken:      desc.LeaseToken,
		})
	}
	emitSubclaimAcquired(ctx, args, parentID, in.InstanceID, in.HolderNodeID, in.ProducerName, len(out), tx)
	return out, nil
}

// @concept: fan-out
// @concept: claim-tree
// @story: sub-claim-payload-substitution
func reuseLinkedSubClaim(
	ctx context.Context, args RunArgs, claimSpec claimproducer.ClaimSpec, cand persistence.Candidate, producer claimproducer.ClaimProducer, acquired []AcquiredLock, livenessInterval time.Duration, tx persistence.Tx,
) (AcquiredLock, bool, error) {
	rows, err := args.ClaimHandles.ListByNodeRun(ctx, cand.NodeRunID, tx)
	if err != nil {
		return AcquiredLock{}, false, fmt.Errorf("reuseLinkedSubClaim: ListByNodeRun: %w", err)
	}
	inRun := make(map[shared.UUID]bool, len(rows))
	for i := range rows {
		inRun[rows[i].ID] = true
	}
	for i := range rows {
		row := rows[i]
		if row.ParentClaimHandleID == nil || inRun[*row.ParentClaimHandleID] {
			continue
		}
		if row.State != spec.ClaimHandleStateActive || row.LockKind != persistence.LockKindScope {
			continue
		}
		if row.ProducerName == nil || *row.ProducerName != claimSpec.ProducerName {
			continue
		}
		if lockAlreadyReused(acquired, row.ID) {
			continue
		}
		if err := renewReusedRunExpiry(ctx, args, cand.NodeRunID, livenessInterval, tx); err != nil {
			return AcquiredLock{}, false, err
		}
		return AcquiredLock{
			Spec:          claimSpec,
			ClaimHandleID: row.ID,
			ClaimResult: claimproducer.ClaimResult{
				ClaimScope:             json.RawMessage(row.ClaimScopeData),
				Address:                json.RawMessage(row.Address),
				Payload:                json.RawMessage(row.Payload),
				RealizedWriteSemantics: claimproducer.WriteSemantics(row.RealizedWriteSemantics),
			},
			Producer:                producer,
			Alias:                   claimSpec.Alias,
			IsHeld:                  row.IsHeld,
			ProducerCandidateHandle: row.ProducerCandidateHandle,
			ProducerLeaseToken:      row.ProducerLeaseToken,
		}, true, nil
	}
	return AcquiredLock{}, false, nil
}

func emitSubclaimBeginCandidate(
	ctx context.Context, args RunArgs, parentID, subID, instanceID, holderNodeID shared.UUID, producerName string, candidateHandleSize int, tx persistence.Tx,
) {
	if args.Persist == nil {
		return
	}
	nodeID := holderNodeID
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		InstanceID: &instanceID,
		NodeID:     &nodeID,
		Kind:       events.KindSubclaimBeginCandidate(),
		Payload: eventpayload.New(&genv1.SubClaimBeginCandidatePayload{
			ParentClaimHandleId:      parentID.String(),
			SubClaimHandleId:         subID.String(),
			ProducerName:             producerName,
			CandidateHandleSizeBytes: int64(candidateHandleSize),
		}),
	}, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("AcquireSubClaims: event append failed",
			"kind", "subclaim.begin_candidate",
			"sub_claim_handle_id", subID.String(),
			"error", err.Error())
	}
}

func emitSubclaimAcquired(
	ctx context.Context, args RunArgs, parentID, instanceID, holderNodeID shared.UUID, producerName string, subScopeCount int, tx persistence.Tx,
) {
	if args.Persist == nil || subScopeCount == 0 {
		return
	}
	nodeID := holderNodeID
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		InstanceID: &instanceID,
		NodeID:     &nodeID,
		Kind:       events.KindSubclaimAcquired(),
		Payload: eventpayload.New(&genv1.SubClaimAcquiredPayload{
			ParentClaimHandleId:     parentID.String(),
			SubScopeDescriptorCount: int32(subScopeCount),
			ProducerName:            producerName,
		}),
	}, tx); err != nil && args.Logger != nil {
		args.Logger.Warn("AcquireSubClaims: event append failed",
			"kind", "subclaim.acquired",
			"parent_claim_handle_id", parentID.String(),
			"error", err.Error())
	}
}
