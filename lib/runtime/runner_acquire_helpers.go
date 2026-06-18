// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @source: lib/runtime/runner_acquire.go

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @concept: fan-out
// @concept: claim-tree
func acquireFanOutIfDeclared(
	ctx context.Context, args RunArgs, tx persistence.Tx, instanceID shared.UUID, out *acquisition,
	cand persistence.Candidate, nodeDef *node.TemplateNodeDef,
	acquiredLocks []AcquiredLock, livenessInterval time.Duration,
) error {
	if nodeDef == nil || nodeDef.FanOut == nil {
		return nil
	}
	// @concept: fan-out
	// @concept: run-scope
	if scopes := args.Persist.RunScopes(); scopes != nil {
		if rs, err := scopes.GetByID(ctx, tx, out.RunScopeID); err == nil && rs != nil && rs.ParentRunID != nil {
			return nil
		}
	}
	fanOutClaim := nodeDef.FanOut.Claim
	var parent *AcquiredLock
	for i := range acquiredLocks {
		if acquiredLocks[i].Alias == fanOutClaim {
			parent = &acquiredLocks[i]
			break
		}
	}
	if parent == nil {
		return nil
	}
	parentClaimSpec, ok := parent.Spec.(claimproducer.ClaimSpec)
	if !ok {
		args.Logger.Warn("tryAcquire: fan-out alias references non-claim spec; ignored",
			"node_id", cand.NodeID.String(),
			"alias", fanOutClaim)
		return nil
	}
	frameID := cand.FrameID
	partitionRequest, err := substituteFanOutPartitionRequest(ctx, args, tx, frameID, out, nodeDef.FanOut.PartitionRequest)
	if err != nil {
		args.Logger.Warn("tryAcquire: fan-out partition_request substitution failed",
			"node_id", cand.NodeID.String(),
			"error", err.Error())
		return fmt.Errorf("acquireFanOutIfDeclared: partition_request substitution: %w", err)
	}
	subClaims, err := AcquireSubClaims(ctx, args, tx, AcquireSubClaimsInput{
		ParentClaimHandleID: parent.ClaimHandleID,
		ParentClaimScope:    parent.ClaimResult.ClaimScope,
		ProducerName:        parentClaimSpec.ProducerName,
		NodeRunID:           cand.DispatchID,
		HolderNodeID:        cand.NodeID,
		HolderSupervisorID:  args.SupervisorID,
		InstanceID:          instanceID,
		FrameID:             &frameID,
		LivenessInterval:    livenessInterval,
		PartitionRequest:    partitionRequest,
		// @concept: claim-lifetime
		Lifetime: spec.ClaimLifetime(parentClaimSpec.Lifetime),
		ParentIsHeld: parent.IsHeld,
		AggregationPolicy: nodeDef.FanOut.ErrorPolicy,
	})
	if err != nil {
		args.Logger.Warn("tryAcquire: fan-out sub-claim acquisition failed",
			"node_id", cand.NodeID.String(),
			"producer", parentClaimSpec.ProducerName,
			"error", err.Error())
		return fmt.Errorf("acquireFanOutIfDeclared: %w", err)
	}
	out.SubClaims = subClaims
	return nil
}

// @concept: fan-out
func substituteFanOutPartitionRequest(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	frameID shared.UUID, out *acquisition, partitionRequest string,
) ([]byte, error) {
	resolveCtx := attributes.ResolveContext{
		Params: instanceParamsRaw(out),
		Claim:  out.HeldClaims,
		RegistryDeclaredTypes: declaredMessageTypesForTemplate(ctx, args, out.TemplateHash, tx),
	}
	payload, mtype := triggerMessageForFrame(ctx, args, tx, frameID)
	if len(payload) > 0 {
		resolveCtx.TriggerMessagePayload = payload
	}
	if mtype != "" {
		resolveCtx.TriggerMessageType = mtype
	}
	val, err := attributes.SubstituteValue(partitionRequest, resolveCtx)
	if err != nil {
		return nil, err
	}
	if s, ok := val.(string); ok {
		return []byte(s), nil
	}
	b, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("marshal substituted partition_request: %w", err)
	}
	return b, nil
}

func instanceParamsRaw(out *acquisition) json.RawMessage {
	if out == nil || len(out.InstanceParams) == 0 {
		return nil
	}
	b, err := json.Marshal(out.InstanceParams)
	if err != nil {
		return nil
	}
	return b
}

func triggerMessageForFrame(
	ctx context.Context, args RunArgs, tx persistence.Tx, frameID shared.UUID,
) (json.RawMessage, string) {
	if args.Persist == nil || args.Persist.Messages() == nil {
		return nil, ""
	}
	rows, err := args.Persist.Messages().ListDeliveredForFrame(ctx, tx, frameID)
	if err != nil {
		args.Logger.Warn("trigger-message lookup failed; substitution falls back to ErrMissingSource",
			"frame_id", frameID.String(),
			"error", err.Error())
		return nil, ""
	}
	if len(rows) != 1 {
		return nil, ""
	}
	return rows[0].Payload, rows[0].Type
}

// @concept: executor
func loadScratchIntoAcquisition(
	ctx context.Context, args RunArgs, tx persistence.Tx, out *acquisition,
	cand persistence.Candidate,
) {
	inline, handle, handleBackend, err := args.Queue.LoadScratchInTx(ctx, tx, cand.DispatchID)
	if err != nil {
		args.Logger.Warn("tryAcquire: LoadScratchInTx failed; passing empty scratch to executor",
			"dispatch_id", cand.DispatchID.String(), "error", err.Error())
		return
	}
	if handle == "" {
		out.Scratch = inline
		return
	}
	if args.Blob == nil {
		args.Logger.Warn("tryAcquire: spilled scratch but no BlobBackend configured; passing empty scratch to executor",
			"node_id", cand.NodeID.String(),
			"handle_backend", handleBackend)
		return
	}
	if args.Blob.Name() != handleBackend {
		args.Logger.Warn("tryAcquire: blob backend mismatch on scratch; passing empty scratch to executor",
			"node_id", cand.NodeID.String(),
			"current_backend", args.Blob.Name(),
			"handle_backend", handleBackend)
		return
	}
	b, berr := args.Blob.Read(ctx, persistence.Handle(handle))
	if berr != nil {
		args.Logger.Warn("tryAcquire: blob fetch for scratch failed; passing empty scratch to executor",
			"node_id", cand.NodeID.String(), "error", berr.Error())
		return
	}
	out.Scratch = b
}
