// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		rs, err := scopes.GetByID(ctx, tx, out.RunScopeID)
		if err != nil {
			return fmt.Errorf("acquireFanOutIfDeclared: load run scope %s: %w", out.RunScopeID, err)
		}
		if rs != nil && rs.ParentNodeRunID != nil && rs.PartitionKey != "" {
			return nil
		}
	}
	fanOutClaim := nodeDef.FanOut.Claim
	parent, err := resolveFanOutParentClaim(ctx, args, tx, instanceID, cand, nodeDef, acquiredLocks, fanOutClaim)
	if err != nil {
		return fmt.Errorf("acquireFanOutIfDeclared: resolve fan_out.claim %q: %w", fanOutClaim, err)
	}
	if parent == nil {
		return nil
	}
	frameID := cand.FrameID
	partitionRequest, err := substituteFanOutPartitionRequest(ctx, args, tx, frameID, out, acquiredLocks, nodeDef.FanOut.PartitionRequest)
	if err != nil {
		args.Logger.Warn("tryAcquire: fan-out partition_request substitution failed",
			"node_id", cand.NodeID.String(),
			"error", err.Error())
		out.FanOutSubstitutionErr = fmt.Errorf("partition_request substitution: %w", err).Error()
		return errAcquireFanOutSubstitutionFailed
	}
	subClaims, err := AcquireSubClaims(ctx, args, tx, AcquireSubClaimsInput{
		ParentClaimHandleID: parent.ClaimHandleID,
		ParentClaimScope:    parent.ClaimScope,
		ProducerName:        parent.ProducerName,
		NodeRunID:           cand.NodeRunID,
		HolderNodeID:        cand.NodeID,
		HolderSupervisorID:  args.SupervisorID,
		InstanceID:          instanceID,
		FrameID:             &frameID,
		LivenessInterval:    livenessInterval,
		PartitionRequest:    partitionRequest,
		// @concept: claim-lifetime
		Lifetime:          parent.Lifetime,
		ParentIsHeld:      parent.IsHeld,
		AggregationPolicy: nodeDef.FanOut.ErrorPolicy,
		ParentIntent:      parent.Intent,
	})
	if err != nil {
		args.Logger.Warn("tryAcquire: fan-out sub-claim acquisition failed",
			"node_id", cand.NodeID.String(),
			"producer", parent.ProducerName,
			"error", err.Error())
		return fmt.Errorf("acquireFanOutIfDeclared: %w", err)
	}
	out.SubClaims = subClaims
	return nil
}

// @concept: fan-out
// @concept: claim-co-holdership
type fanOutParentClaim struct {
	ClaimHandleID shared.UUID
	ClaimScope    json.RawMessage
	ProducerName  string
	Lifetime      spec.ClaimLifetime
	IsHeld        bool
	Intent        string
}

// @concept: fan-out
// @concept: claim-co-holdership
func resolveFanOutParentClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx, instanceID shared.UUID,
	cand persistence.Candidate, nodeDef *node.TemplateNodeDef,
	acquiredLocks []AcquiredLock, alias string,
) (*fanOutParentClaim, error) {
	for i := range acquiredLocks {
		if acquiredLocks[i].Alias != alias {
			continue
		}
		parentClaimSpec, ok := acquiredLocks[i].Spec.(claimproducer.ClaimSpec)
		if !ok {
			args.Logger.Warn("tryAcquire: fan-out alias references non-claim spec; ignored",
				"node_id", cand.NodeID.String(),
				"alias", alias)
			return nil, nil
		}
		return &fanOutParentClaim{
			ClaimHandleID: acquiredLocks[i].ClaimHandleID,
			ClaimScope:    acquiredLocks[i].ClaimResult.ClaimScope,
			ProducerName:  parentClaimSpec.ProducerName,
			Lifetime:      spec.ClaimLifetime(parentClaimSpec.Lifetime),
			IsHeld:        acquiredLocks[i].IsHeld,
			Intent:        string(parentClaimSpec.Intent),
		}, nil
	}
	binding, ok := nodeDef.Holds[alias]
	if !ok || binding.From == "" {
		return nil, nil
	}
	upstreamNode, err := findInstanceNodeByType(ctx, args, tx, instanceID, binding.From)
	if err != nil {
		return nil, fmt.Errorf("resolveFanOutParentClaim: find upstream node: %w", err)
	}
	if upstreamNode == nil {
		return nil, nil
	}
	tmplSpec, err := loadTemplateSpec(ctx, args, tx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("resolveFanOutParentClaim: load template: %w", err)
	}
	if tmplSpec == nil {
		return nil, nil
	}
	lh, err := lookupClaimHandleForAlias(ctx, args, tx, upstreamNode.ID, tmplSpec, binding.From, alias, cand.FrameID)
	if err != nil {
		return nil, fmt.Errorf("resolveFanOutParentClaim: lookup claim handle: %w", err)
	}
	if lh == nil {
		return nil, nil
	}
	producerName := ""
	if lh.ProducerName != nil {
		producerName = *lh.ProducerName
	}
	intent := ""
	if lh.Intent != nil {
		intent = *lh.Intent
	}
	return &fanOutParentClaim{
		ClaimHandleID: lh.ID,
		ClaimScope:    lh.ClaimScopeData,
		ProducerName:  producerName,
		Lifetime:      lh.Lifetime,
		IsHeld:        lh.IsHeld,
		Intent:        intent,
	}, nil
}

// @concept: fan-out
// @decision: substitution-grammar-closed
func substituteFanOutPartitionRequest(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	frameID shared.UUID, out *acquisition, acquiredLocks []AcquiredLock,
	partitionRequest string,
) ([]byte, error) {
	claims := map[string]claimproducer.ClaimResult{}
	for _, lk := range acquiredLocks {
		if lk.Alias == "" {
			continue
		}
		claims[lk.Alias] = lk.ClaimResult
	}
	for alias, held := range out.HeldClaims {
		if _, alreadyPresent := claims[alias]; alreadyPresent {
			continue
		}
		claims[alias] = held
	}
	instanceParams, err := instanceParamsRaw(out)
	if err != nil {
		return nil, fmt.Errorf("substituteFanOutPartitionRequest: marshal instance params: %w", err)
	}
	resolveCtx, err := buildResolveContextForAcquisition(
		ctx, args, tx,
		frameID, out.NodeRunID,
		out.TemplateHash,
		instanceParams,
		claims,
	)
	if err != nil {
		return nil, err
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

func instanceParamsRaw(out *acquisition) (json.RawMessage, error) {
	if out == nil || len(out.InstanceParams) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(out.InstanceParams)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// @concept: executor
func loadScratchIntoAcquisition(
	ctx context.Context, args RunArgs, tx persistence.Tx, out *acquisition,
	cand persistence.Candidate,
) {
	inline, handle, handleBackend, err := args.Queue.LoadScratchInTx(ctx, tx, cand.NodeRunID)
	if err != nil {
		args.Logger.Warn("tryAcquire: LoadScratchInTx failed; passing empty scratch to executor",
			"dispatch_id", cand.NodeRunID.String(), "error", err.Error())
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
