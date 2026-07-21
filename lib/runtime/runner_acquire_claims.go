// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	fspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

func acquireClaim(
	ctx context.Context, args RunArgs, instanceID shared.UUID, spec claimproducer.ClaimSpec, cand persistence.Candidate, livenessInterval time.Duration, heldSubgraphs []node.HoldingSubgraph, acquired []AcquiredLock, tx persistence.Tx,
) (AcquiredLock, openResult, error) {
	acquireStart := args.Clock.Now()
	s, ok := args.StoreRegistry.GetWithContext(ctx, spec.ProducerName, instanceID.String())
	if !ok {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: unknown store %q", spec.ProducerName)
	}
	scopeInitial, err := json.Marshal(spec.Selector)
	if err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: marshal selector: %w", err)
	}
	if err := args.AdvisoryLocker.TakeClaimScopeLock(ctx, spec.ProducerName, scopeInitial, tx); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: TakeClaimScopeLock: %w", err)
	}
	// @concept: parked-state
	if al, reused, rErr := reuseHeldRunClaim(ctx, args, spec, cand, scopeInitial, s, acquired, livenessInterval, tx); rErr != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: reuse held run claim: %w", rErr)
	} else if reused {
		metricsOf(args).IncClaimAcquisition(spec.ProducerName, "acquired")
		metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())
		return al, openResultAcquired, nil
	}
	// @concept: fan-out
	if al, reused, rErr := reuseLinkedSubClaim(ctx, args, spec, cand, s, acquired, livenessInterval, tx); rErr != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: %w", rErr)
	} else if reused {
		metricsOf(args).IncClaimAcquisition(spec.ProducerName, "acquired")
		metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())
		return al, openResultAcquired, nil
	}
	// @story: claim-handoff-durable
	conflicted, persistent, err := evaluateClaimScopeConflict(ctx, args, s, spec, cand, tx)
	if err != nil {
		return AcquiredLock{}, openResultBail, err
	}
	if conflicted {
		if persistent {
			metricsOf(args).IncClaimAcquisition(spec.ProducerName, "unavailable")
			metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())
			return AcquiredLock{}, openResultUnavailable, nil
		}
		return AcquiredLock{}, openResultBail, nil
	}

	rowID := uuid.New()
	frameID := cand.FrameID
	nodeRunID := cand.NodeRunID
	producerNameCopy := spec.ProducerName
	intentCopy := string(spec.Intent)
	subgraph, hasSubgraph := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, spec.Alias)
	isHeld := hasSubgraph && subgraph.IsHeld()
	in := persistence.ClaimHandleInsertInput{
		ID:                 rowID,
		NodeRunID:          &nodeRunID,
		LockKind:           persistence.LockKindScope,
		ProducerName:       &producerNameCopy,
		ClaimScopeData:     scopeInitial,
		Intent:             &intentCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ExpiresAt:          claimExpiryFromLiveness(args.Clock.Now(), livenessInterval),
		FrameID:            &frameID,
		IsHeld:             isHeld,
		// @concept: claim-lifetime
		Lifetime: fspec.ClaimLifetime(spec.Lifetime),
	}
	if err := args.ClaimHandles.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: Insert: %w", err)
	}

	// @concept: terminal-resolution
	if err := producerVerbOutboxBarrier(ctx, args, s, spec.ProducerName, scopeInitial, tx); err != nil {
		var pcErr *peer.ProducerCallError
		if errors.As(err, &pcErr) {
			recordClaimAcquisitionErrored(args, spec.ProducerName, acquireStart)
			return AcquiredLock{}, openResultErrored, pcErr
		}
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: %w", err)
	}
	claimID := claimproducer.ClaimID(rowID.String())
	openCtx := peer.WithServiceName(ctx, spec.ProducerName)
	outcome, err := s.Open(openCtx, claimID, spec)
	if err != nil {
		var pcErr *peer.ProducerCallError
		if errors.As(err, &pcErr) {
			recordClaimAcquisitionErrored(args, spec.ProducerName, acquireStart)
			return AcquiredLock{}, openResultErrored, pcErr
		}
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: Open(%s): %w", spec.ProducerName, err)
	}
	if !outcome.Available {
		metricsOf(args).IncClaimAcquisition(spec.ProducerName, "unavailable")
		metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())
		return AcquiredLock{UnavailableClass: outcome.UnavailableClass}, openResultUnavailable, nil
	}
	cr := outcome.Result

	if err := args.ClaimHandles.UpdateAddress(ctx, rowID, args.SupervisorID, cr.Address, tx); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateAddress: %w", err)
	}
	if len(cr.ClaimScope) > 0 && string(cr.ClaimScope) != string(scopeInitial) {
		if err := args.ClaimHandles.UpdateClaimScope(ctx, rowID, args.SupervisorID, cr.ClaimScope, tx); err != nil {
			return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateClaimScope: %w", err)
		}
	}
	// @concept: claim-co-holdership
	if len(cr.Payload) > 0 {
		if err := args.ClaimHandles.UpdatePayload(ctx, rowID, args.SupervisorID, cr.Payload, tx); err != nil {
			return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdatePayload: %w", err)
		}
	}
	if cr.RealizedWriteSemantics != "" {
		if err := args.ClaimHandles.UpdateRealizedWriteSemantics(ctx, rowID, args.SupervisorID, string(cr.RealizedWriteSemantics), tx); err != nil {
			return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateRealizedWriteSemantics: %w", err)
		}
	}

	if err := insertHeldClaimHoldersAtAcquire(ctx, args, rowID, cand, isHeld, tx); err != nil {
		return AcquiredLock{}, openResultBail, err
	}

	metricsOf(args).IncClaimAcquisition(spec.ProducerName, "acquired")
	metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())

	return AcquiredLock{
		Spec:          spec,
		ClaimHandleID: rowID,
		ClaimResult:   cr,
		Producer:      s,
		Alias:         spec.Alias,
		IsHeld:        isHeld,
	}, openResultAcquired, nil
}

func recordClaimAcquisitionErrored(args RunArgs, producerName string, acquireStart time.Time) {
	metricsOf(args).IncClaimAcquisition(producerName, "errored")
	metricsOf(args).ObserveClaimAcquisitionLatency(producerName, args.Clock.Now().Sub(acquireStart).Seconds())
}

// @story: claim-handoff-durable
func evaluateClaimScopeConflict(
	ctx context.Context, args RunArgs, s claimproducer.ClaimProducer, spec claimproducer.ClaimSpec, cand persistence.Candidate, tx persistence.Tx,
) (bool, bool, error) {
	holders, err := args.ClaimHandles.ListByProducerClaimScope(ctx, spec.ProducerName, tx)
	if err != nil {
		return false, false, fmt.Errorf("evaluateClaimScopeConflict: ListByProducerClaimScope: %w", err)
	}
	candidateScope, err := json.Marshal(spec.Selector)
	if err != nil {
		return false, false, err
	}
	caps, err := s.Capabilities(ctx)
	if err != nil {
		return false, false, fmt.Errorf("evaluateClaimScopeConflict: Capabilities(%s): %w", spec.ProducerName, err)
	}
	var (
		sawConflict             bool
		sawDurableCommittedConf bool
	)
	for _, h := range holders {
		if h.NodeRunID != nil && *h.NodeRunID == cand.NodeRunID {
			continue
		}
		// @concept: asset
		if h.HolderNodeID == cand.NodeID &&
			h.State == fspec.ClaimHandleStateCommitted &&
			h.Lifetime == fspec.ClaimLifetimeDurable {
			continue
		}
		conflicts, cErr := scopesConflict(ctx, s, caps, candidateScope, h.ClaimScopeData)
		if cErr != nil {
			return false, false, fmt.Errorf("evaluateClaimScopeConflict: ScopesConflict(%s): %w", spec.ProducerName, cErr)
		}
		if !conflicts {
			continue
		}
		var holderIntent claimproducer.Intent
		if h.Intent != nil {
			holderIntent = claimproducer.Intent(*h.Intent)
		}
		holderRWS, ok := claimproducer.ParseWriteSemantics(h.RealizedWriteSemantics)
		if !ok {
			return false, false, fmt.Errorf("evaluateClaimScopeConflict: claim handle %s has no realized write-semantics yet (holder open still in flight)", h.ID)
		}
		if !locks.ModeCoexists(spec.Intent, holderIntent, holderRWS) {
			sawConflict = true
			if h.State == fspec.ClaimHandleStateCommitted && h.Lifetime == fspec.ClaimLifetimeDurable {
				sawDurableCommittedConf = true
			}
		}
	}
	return sawConflict, sawDurableCommittedConf, nil
}

func scopesConflict(
	ctx context.Context, s claimproducer.ClaimProducer, caps claimproducer.Capabilities, a, b []byte,
) (bool, error) {
	if !caps.SupportsScopesConflict {
		return locks.ClaimScopesByteEqual(a, b), nil
	}
	return s.ScopesConflict(ctx, a, b)
}
