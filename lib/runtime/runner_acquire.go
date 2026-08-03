// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

func producerErrorClassOf(err error) string {
	var pcErr *peer.ProducerCallError
	if errors.As(err, &pcErr) {
		return pcErr.ErrorClass
	}
	return ""
}

// @concept: attribute
func graphContainingNodeType(graphs []spec.GraphSpec, nodeType string) string {
	for _, g := range graphs {
		for _, n := range g.Nodes {
			if n.Type == nodeType {
				return g.Name
			}
		}
	}
	return spec.MainGraphName
}

type acquisition struct {
	NodeRunID  shared.UUID
	NodeID     shared.UUID
	InstanceID shared.UUID
	NodeType   string
	Executor   string
	// @concept: attribute
	GraphName string

	// @concept: run-scope
	RunScopeID shared.UUID

	// @concept: run-scope
	PriorNodeRunID *shared.UUID
	// @concept: run-scope
	PriorDispatchDisposition string

	FrameID                    shared.UUID
	Locks                      []AcquiredLock
	NodeDef                    *node.TemplateNodeDef
	HeldSubgraphs              []node.HoldingSubgraph
	InstanceParams             map[string]any
	InstanceAttributeOverrides map[string]any

	// @concept: attribute
	TemplateAttributeDefaults map[string]any

	PartialLocks []AcquiredLock

	UnavailableSpec  claimproducer.ClaimSpec
	UnavailableClass string

	ErroredSpec        claimproducer.ClaimSpec
	ProducerErrorClass string

	// @concept: error-policy
	RetryDecision *policyDecision

	SubClaims []SubClaim

	// @concept: claim-co-holdership
	HeldClaims map[string]claimproducer.ClaimResult

	// @concept: attribute
	MergedAttributes map[string]any

	TemplateHash string

	// @concept: executor
	Scratch []byte

	// @concept: fan-out
	FanOutSubstitutionErr string

	// @decision: substitution-failure-routes-with-substitution
	LockSpecSubstitutionSite      string
	LockSpecSubstitutionDirective string
	LockSpecSubstitutionErr       string
}

type openResult int

const (
	openResultAcquired openResult = iota
	openResultUnavailable
	openResultErrored
	openResultBail
)

var errAcquireUnavailable = fmt.Errorf("supervisor: acquire bailed on Unavailable claim (sentinel)")

var errAcquireProducerErrored = fmt.Errorf("supervisor: acquire bailed on producer-faulted claim (sentinel)")

var errAcquireRestampLost = fmt.Errorf("supervisor: acquire lost sub-claim restamp CAS (sentinel)")

var errAcquireNilFrameID = fmt.Errorf("supervisor: acquire bailed on candidate with null frame_id (sentinel)")

var errAcquireFanOutSubstitutionFailed = fmt.Errorf("supervisor: acquire bailed on fan-out partition_request substitution failure (sentinel)")

var errAcquireLockSpecSubstitutionFailed = fmt.Errorf("supervisor: acquire bailed on lock-spec substitution failure (sentinel)")

const defaultSelectCandidatesLimit = 8

func acquireCandidate(ctx context.Context, args RunArgs, livenessInterval time.Duration) (acquisition, bool, error) {
	limit := args.SelectCandidatesLimit
	if limit <= 0 {
		limit = defaultSelectCandidatesLimit
	}
	var (
		cursorEnqueued time.Time
		cursorID       shared.UUID
	)
	for {
		candidates, err := selectCandidatesShortTx(ctx, args, cursorEnqueued, cursorID)
		if err != nil {
			return acquisition{}, false, err
		}
		if len(candidates) == 0 {
			return acquisition{}, false, nil
		}
		acq, ok, err := tryAcquireBatch(ctx, args, candidates, livenessInterval)
		if err != nil || ok {
			return acq, ok, err
		}
		if len(candidates) < limit {
			return acquisition{}, false, nil
		}
		last := candidates[len(candidates)-1]
		cursorEnqueued, cursorID = last.EnqueuedAt, last.NodeRunID
	}
}

func tryAcquireBatch(
	ctx context.Context, args RunArgs, candidates []persistence.Candidate,
	livenessInterval time.Duration,
) (acquisition, bool, error) {
	for _, cand := range candidates {
		acq, ok, err := acquireOneCandidateWithRetry(ctx, args, cand, livenessInterval)
		if err != nil {
			return acquisition{}, false, err
		}
		if !ok {
			continue
		}
		if args.PostCommitHook != nil {
			args.PostCommitHook(ctx)
		}
		if !verifyBeforeRun(ctx, args, acq) {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		var promoted bool
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			p, err := args.Queue.PromoteClaimedToRunning(ctx, acq.NodeRunID, args.SupervisorID, tx)
			promoted = p
			return err
		}); err != nil {
			return acquisition{}, false, fmt.Errorf("tryAcquire: PromoteClaimedToRunning: %w", err)
		}
		if !promoted {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		for _, lk := range acq.Locks {
			if nls, isNamed := lk.Spec.(locks.NamedLockSpec); isNamed {
				metricsOf(args).IncNamedLockAcquisition(namedLockMetricLabel(nls), "acquired")
			}
		}
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if !resumesStaleRecoveredDispatch(acq) {
				if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
					NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
					Kind: events.KindWorkStarted(), Payload: map[string]any{
						"supervisor_id": args.SupervisorID,
						"dispatch_id":   acq.NodeRunID.String(),
					},
				}, tx); err != nil {
					return err
				}
			}
			for _, lk := range acq.Locks {
				if err := emitLockAcquired(ctx, args, acq, lk, tx); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			args.Logger.Warn("tryAcquire: post-acquisition audit tx failed; work_started/lock_acquired events lost",
				"dispatch_id", acq.NodeRunID.String(),
				"node_id", acq.NodeID.String(),
				"error", err.Error())
		}
		return acq, true, nil
	}
	return acquisition{}, false, nil
}

// @story: work-completed-emitted
// @decision: prior-stale-recovery-rename
func resumesStaleRecoveredDispatch(acq acquisition) bool {
	return acq.PriorDispatchDisposition == "stale_recovery" &&
		acq.PriorNodeRunID != nil && *acq.PriorNodeRunID == acq.NodeRunID
}

// @decision: in-place-retry
func acquireOneCandidateWithRetry(
	ctx context.Context, args RunArgs, cand persistence.Candidate,
	livenessInterval time.Duration,
) (acquisition, bool, error) {
	for {
		acq, ok, err := tryAcquireWithTx(ctx, args, cand, livenessInterval)
		if err == errAcquireUnavailable {
			decision := handleAcquireUnavailable(ctx, args, acq, cand)
			if decision != nil && decision.IsRetry() {
				if delay := time.Duration(decision.DelayMs) * time.Millisecond; delay > 0 {
					select {
					case <-ctx.Done():
						return acquisition{}, false, ctx.Err()
					case <-time.After(delay):
					}
				}
				continue
			}
			return acquisition{}, false, nil
		}
		if err == errAcquireProducerErrored {
			decision := handleAcquireProducerError(ctx, args, acq, cand)
			if decision != nil && decision.IsRetry() {
				if delay := time.Duration(decision.DelayMs) * time.Millisecond; delay > 0 {
					select {
					case <-ctx.Done():
						return acquisition{}, false, ctx.Err()
					case <-time.After(delay):
					}
				}
				continue
			}
			return acquisition{}, false, nil
		}
		if err == errAcquireRestampLost {
			args.Logger.Info("tryAcquire: sub-claim holder restamp lost CAS to concurrent supervisor; skipping candidate",
				"dispatch_id", cand.NodeRunID.String(),
				"node_id", cand.NodeID.String())
			return acquisition{}, false, nil
		}
		if err == errAcquireNilFrameID {
			decision := handleAcquireNilFrameID(ctx, args, acq, cand)
			if decision != nil && decision.IsRetry() {
				if delay := time.Duration(decision.DelayMs) * time.Millisecond; delay > 0 {
					select {
					case <-ctx.Done():
						return acquisition{}, false, ctx.Err()
					case <-time.After(delay):
					}
				}
				continue
			}
			return acquisition{}, false, nil
		}
		if err == errAcquireFanOutSubstitutionFailed {
			decision := handleAcquireFanOutSubstitutionFailed(ctx, args, acq, cand)
			if decision != nil && decision.IsRetry() {
				if delay := time.Duration(decision.DelayMs) * time.Millisecond; delay > 0 {
					select {
					case <-ctx.Done():
						return acquisition{}, false, ctx.Err()
					case <-time.After(delay):
					}
				}
				continue
			}
			return acquisition{}, false, nil
		}
		if err == errAcquireLockSpecSubstitutionFailed {
			decision := handleAcquireLockSpecSubstitutionFailed(ctx, args, acq, cand)
			if decision != nil && decision.IsRetry() {
				if delay := time.Duration(decision.DelayMs) * time.Millisecond; delay > 0 {
					select {
					case <-ctx.Done():
						return acquisition{}, false, ctx.Err()
					case <-time.After(delay):
					}
				}
				continue
			}
			return acquisition{}, false, nil
		}
		if err != nil {
			return acquisition{}, false, err
		}
		return acq, ok, nil
	}
}

func selectCandidatesShortTx(
	ctx context.Context, args RunArgs,
	cursorEnqueued time.Time, cursorID shared.UUID,
) ([]persistence.Candidate, error) {
	limit := args.SelectCandidatesLimit
	if limit <= 0 {
		limit = defaultSelectCandidatesLimit
	}
	var candidates []persistence.Candidate
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := args.Queue.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit:                limit,
			CursorEnqueuedAfter:  cursorEnqueued,
			CursorAfterNodeRunID: cursorID,
		}, tx)
		if err != nil {
			return err
		}
		candidates = out
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("acquireCandidate: SelectCandidates: %w", err)
	}
	return candidates, nil
}

func tryAcquireWithTx(
	ctx context.Context, args RunArgs, cand persistence.Candidate,
	livenessInterval time.Duration,
) (acquisition, bool, error) {
	var (
		acq acquisition
		ok  bool
	)
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var inner error
		acq, ok, inner = tryAcquire(ctx, args, cand, livenessInterval, tx)
		if inner != nil {
			return inner
		}
		if !ok {
			return errTryAcquireRollback
		}
		return nil
	})
	if err == errAcquireUnavailable {
		return acq, false, errAcquireUnavailable
	}
	if err == errAcquireProducerErrored {
		return acq, false, errAcquireProducerErrored
	}
	if err == errAcquireRestampLost {
		abandonPartialLocks(ctx, args, acq.PartialLocks)
		return acquisition{}, false, errAcquireRestampLost
	}
	if err == errAcquireNilFrameID {
		return acq, false, errAcquireNilFrameID
	}
	if err == errAcquireFanOutSubstitutionFailed {
		return acq, false, errAcquireFanOutSubstitutionFailed
	}
	if err == errAcquireLockSpecSubstitutionFailed {
		abandonPartialLocks(ctx, args, acq.PartialLocks)
		return acq, false, errAcquireLockSpecSubstitutionFailed
	}
	if err != nil && err != errTryAcquireRollback {
		abandonPartialLocks(ctx, args, acq.PartialLocks)
		return acquisition{}, false, fmt.Errorf("tryAcquireWithTx: %w", err)
	}
	if err == errTryAcquireRollback {
		abandonPartialLocks(ctx, args, acq.PartialLocks)
		return acquisition{}, false, nil
	}
	return acq, ok, nil
}

var errTryAcquireRollback = fmt.Errorf("supervisor: tryAcquire rollback (sentinel)")

func tryAcquire(
	ctx context.Context, args RunArgs, cand persistence.Candidate, livenessInterval time.Duration, tx persistence.Tx,
) (acquisition, bool, error) {
	nd, err := args.Persist.Nodes().Get(ctx, cand.NodeID, tx)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: nodes.Get: %w", err)
	}
	if nd == nil {
		return acquisition{}, false, nil
	}
	inst, err := args.Persist.Instances().Get(ctx, nd.InstanceID, tx)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: instances.Get: %w", err)
	}
	tmpl := lookupTemplate(ctx, args, inst, tx)
	nodeDef := LookupNodeDef(tmpl, nd.NodeType)
	templateAttributeDefaults := templateAttributeDefaultsFor(tmpl, nd.Executor)
	// @concept: attribute
	graphName := spec.MainGraphName
	if tmpl != nil {
		graphName = graphContainingNodeType(tmpl.Graphs, nd.NodeType)
	}
	var runScopeID shared.UUID
	if rt := args.Persist.NodeRunTree(); rt != nil {
		row, err := rt.GetByID(ctx, cand.NodeRunID, tx)
		if err != nil {
			return acquisition{}, false, fmt.Errorf("tryAcquire: run-tree GetByID: %w", err)
		}
		if row == nil {
			args.Logger.Info("tryAcquire: run row absent (retired between selection and acquire); skipping candidate",
				"dispatch_id", cand.NodeRunID.String(),
				"node_id", cand.NodeID.String())
			return acquisition{}, false, nil
		}
		runScopeID = row.RunScopeID
	}
	// @concept: frame
	if cand.FrameID == (shared.UUID{}) {
		out := acquisition{
			NodeRunID:                 cand.NodeRunID,
			NodeID:                    cand.NodeID,
			InstanceID:                nd.InstanceID,
			NodeType:                  nd.NodeType,
			Executor:                  nd.Executor,
			GraphName:                 graphName,
			RunScopeID:                runScopeID,
			PriorNodeRunID:            cand.PriorNodeRunID,
			PriorDispatchDisposition:  cand.PriorDispatchDisposition,
			FrameID:                   cand.FrameID,
			NodeDef:                   nodeDef,
			TemplateAttributeDefaults: templateAttributeDefaults,
		}
		if inst != nil {
			out.InstanceParams = inst.Params
			out.InstanceAttributeOverrides = inst.AttributeOverrides
			out.TemplateHash = inst.TemplateHash
		}
		return out, false, errAcquireNilFrameID
	}
	// @decision: walker-rule-per-sender-node
	// @decision: substitution-failure-routes-with-substitution
	specs, heldClaims, err := buildLockSpecs(ctx, args, nd, nodeDef, tmpl, inst, cand.NodeRunID, cand.FrameID, runScopeID, tx)
	if err != nil {
		var subErr *lockSpecSubstitutionError
		if errors.As(err, &subErr) {
			out := acquisition{
				NodeRunID:                     cand.NodeRunID,
				NodeID:                        cand.NodeID,
				InstanceID:                    nd.InstanceID,
				NodeType:                      nd.NodeType,
				Executor:                      nd.Executor,
				GraphName:                     graphName,
				RunScopeID:                    runScopeID,
				PriorNodeRunID:                cand.PriorNodeRunID,
				PriorDispatchDisposition:      cand.PriorDispatchDisposition,
				FrameID:                       cand.FrameID,
				NodeDef:                       nodeDef,
				TemplateAttributeDefaults:     templateAttributeDefaults,
				LockSpecSubstitutionSite:      subErr.Site,
				LockSpecSubstitutionDirective: subErr.Directive,
				LockSpecSubstitutionErr:       subErr.Cause.Error(),
			}
			if inst != nil {
				out.InstanceParams = inst.Params
				out.InstanceAttributeOverrides = inst.AttributeOverrides
				out.TemplateHash = inst.TemplateHash
			}
			return out, false, errAcquireLockSpecSubstitutionFailed
		}
		return acquisition{}, false, err
	}
	sortLockSpecs(specs)

	if err := takeNamedAdvisoryLocks(ctx, args, specs, tx); err != nil {
		return acquisition{}, false, err
	}

	claimed, err := args.Queue.ClaimDispatchRow(ctx, cand.NodeRunID, args.SupervisorID, tx)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: ClaimDispatchRow: %w", err)
	}
	if !claimed {
		return acquisition{}, false, nil
	}

	heldSubgraphs := node.HoldingSubgraphsForTemplate(tmpl)

	acquiredLocks := make([]AcquiredLock, 0, len(specs))
	for _, sp := range specs {
		al, res, err := acquireOneLock(ctx, args, nd.InstanceID, sp, cand, livenessInterval, heldSubgraphs, acquiredLocks, tx)
		if res == openResultErrored {
			erroredSpec, _ := sp.(claimproducer.ClaimSpec)
			out := acquisition{
				NodeRunID:                 cand.NodeRunID,
				NodeID:                    cand.NodeID,
				InstanceID:                nd.InstanceID,
				NodeType:                  nd.NodeType,
				Executor:                  nd.Executor,
				GraphName:                 graphName,
				RunScopeID:                runScopeID,
				PriorNodeRunID:            cand.PriorNodeRunID,
				PriorDispatchDisposition:  cand.PriorDispatchDisposition,
				FrameID:                   cand.FrameID,
				NodeDef:                   nodeDef,
				HeldSubgraphs:             heldSubgraphs,
				PartialLocks:              acquiredLocks,
				ErroredSpec:               erroredSpec,
				ProducerErrorClass:        producerErrorClassOf(err),
				TemplateAttributeDefaults: templateAttributeDefaults,
			}
			if inst != nil {
				out.InstanceParams = inst.Params
				out.InstanceAttributeOverrides = inst.AttributeOverrides
				out.TemplateHash = inst.TemplateHash
			}
			return out, false, errAcquireProducerErrored
		}
		if err != nil {
			return acquisition{PartialLocks: acquiredLocks}, false, err
		}
		switch res {
		case openResultAcquired:
			acquiredLocks = append(acquiredLocks, al)
		case openResultUnavailable:
			unavailableSpec, _ := sp.(claimproducer.ClaimSpec)
			out := acquisition{
				NodeRunID:                 cand.NodeRunID,
				NodeID:                    cand.NodeID,
				InstanceID:                nd.InstanceID,
				NodeType:                  nd.NodeType,
				Executor:                  nd.Executor,
				GraphName:                 graphName,
				RunScopeID:                runScopeID,
				PriorNodeRunID:            cand.PriorNodeRunID,
				PriorDispatchDisposition:  cand.PriorDispatchDisposition,
				FrameID:                   cand.FrameID,
				NodeDef:                   nodeDef,
				HeldSubgraphs:             heldSubgraphs,
				PartialLocks:              acquiredLocks,
				UnavailableSpec:           unavailableSpec,
				UnavailableClass:          al.UnavailableClass,
				TemplateAttributeDefaults: templateAttributeDefaults,
			}
			if inst != nil {
				out.InstanceParams = inst.Params
				out.InstanceAttributeOverrides = inst.AttributeOverrides
				out.TemplateHash = inst.TemplateHash
			}
			return out, false, errAcquireUnavailable
		case openResultBail:
			return acquisition{PartialLocks: acquiredLocks}, false, nil
		}
	}

	out := acquisition{
		NodeRunID:                 cand.NodeRunID,
		NodeID:                    cand.NodeID,
		InstanceID:                nd.InstanceID,
		NodeType:                  nd.NodeType,
		Executor:                  nd.Executor,
		GraphName:                 graphName,
		RunScopeID:                runScopeID,
		PriorNodeRunID:            cand.PriorNodeRunID,
		PriorDispatchDisposition:  cand.PriorDispatchDisposition,
		FrameID:                   cand.FrameID,
		Locks:                     acquiredLocks,
		NodeDef:                   nodeDef,
		HeldSubgraphs:             heldSubgraphs,
		TemplateAttributeDefaults: templateAttributeDefaults,
	}
	if inst != nil {
		out.InstanceParams = inst.Params
		out.InstanceAttributeOverrides = inst.AttributeOverrides
		out.TemplateHash = inst.TemplateHash
	}
	if len(heldClaims) > 0 {
		out.HeldClaims = heldClaims
	}
	if err := acquireFanOutIfDeclared(ctx, args, nd.InstanceID, &out, cand, nodeDef, acquiredLocks, livenessInterval, tx); err != nil {
		if err == errAcquireFanOutSubstitutionFailed {
			out.PartialLocks = acquiredLocks
			return out, false, errAcquireFanOutSubstitutionFailed
		}
		return acquisition{PartialLocks: acquiredLocks}, false, err
	}
	if err := restampLinkedSubClaimHolders(ctx, args, cand, tx); err != nil {
		return acquisition{PartialLocks: acquiredLocks}, false, err
	}
	// @concept: fan-out
	bindLeafCandidateHandles(ctx, args, &out, cand, tx)
	// @concept: claim-co-holdership
	if err := insertCoHolderClaimHoldersAtAcquire(ctx, args, cand, nodeDef, tmpl, tx); err != nil {
		return acquisition{PartialLocks: acquiredLocks}, false, fmt.Errorf("tryAcquire: co-holder rows: %w", err)
	}

	// @concept: executor
	loadScratchIntoAcquisition(ctx, args, &out, cand, tx)
	return out, true, nil
}

// @concept: claim-tree
// @concept: fan-out
func restampLinkedSubClaimHolders(
	ctx context.Context, args RunArgs, cand persistence.Candidate, tx persistence.Tx,
) error {
	ch := args.ClaimHandles
	if ch == nil {
		return nil
	}
	rows, err := ch.ListByNodeRun(ctx, cand.NodeRunID, tx)
	if err != nil {
		return fmt.Errorf("restampLinkedSubClaimHolders: ListByNodeRun: %w", err)
	}
	for i := range rows {
		row := rows[i]
		if row.ParentClaimHandleID == nil || row.State != spec.ClaimHandleStateActive {
			continue
		}
		if row.HolderSupervisorID == nil || *row.HolderSupervisorID == args.SupervisorID {
			continue
		}
		if err := ch.ReassignHolderSupervisor(ctx, row.ID, *row.HolderSupervisorID, args.SupervisorID, tx); err != nil {
			if errors.Is(err, spec.ErrIllegalClaimHandleTransition) {
				return errAcquireRestampLost
			}
			return fmt.Errorf("restampLinkedSubClaimHolders: sub-claim %s (holder %s → %s): %w",
				row.ID, *row.HolderSupervisorID, args.SupervisorID, err)
		}
	}
	return nil
}

// @concept: fan-out
// @concept: data-processing
func bindLeafCandidateHandles(ctx context.Context, args RunArgs, out *acquisition, cand persistence.Candidate, tx persistence.Tx) {
	if out == nil || len(out.Locks) == 0 {
		return
	}
	ch := args.ClaimHandles
	if ch == nil {
		return
	}
	rows, err := ch.ListByNodeRun(ctx, cand.NodeRunID, tx)
	if err != nil {
		args.Logger.Warn("bindLeafCandidateHandles: ListByNodeRun failed; leaf candidate_handle left empty",
			"run_id", cand.NodeRunID.String(),
			"error", err.Error())
		return
	}
	for i := range rows {
		row := rows[i]
		if row.ParentClaimHandleID == nil || len(row.ProducerCandidateHandle) == 0 || row.ProducerName == nil {
			continue
		}
		for j := range out.Locks {
			if _, ok := out.Locks[j].Spec.(claimproducer.ClaimSpec); !ok {
				continue
			}
			if *row.ParentClaimHandleID != out.Locks[j].ClaimHandleID {
				continue
			}
			out.Locks[j].ProducerCandidateHandle = row.ProducerCandidateHandle
			break
		}
	}
}
