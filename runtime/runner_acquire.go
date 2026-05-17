// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Atomic acquisition under stores-redesign-v3 spec §7.3.
//
// Per-candidate try tx (rimsky-side bookkeeping only):
//   - candidate selection (FOR UPDATE SKIP LOCKED) in a short read tx;
//   - per-candidate try tx: in-Go eligibility, advisory locks for
//     named locks, claimant-guarded UPDATE on rimsky_node_runs, scope
//     re-evaluation per store via byte-equal + ModeCoexists, per-spec
//     lock acquisition (Insert + remote Open + UpdateAddress for
//     ClaimSpec; Insert only for NamedLockSpec), held-claim
//     rimsky_claim_holders inserts when the alias is in a held
//     subgraph.
//   - COMMIT, then verify-before-run (separate read), then a second
//     short tx transitioning the node to running.
//
// ClaimProducer.Open is invoked OVER THE WIRE in v3; the store runs its
// own state mutation in its own transaction. Tx-sharing via
// locks.WithTx / TxFromContext is gone.
//
// Two primitives, two types: locks.NamedLockSpec and locks.ClaimSpec.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/node"
)

// resumeMetadata captures the parked metadata for a resumed run. When
// non-nil, the runner attaches a ResumeContext to the ExecuteRequest
// the executor receives. Per plan E4.
type resumeMetadata struct {
	Payload      []byte // Park.payload (post-spill resolution)
	SessionToken string
	Reason       WakeReason
}

// acquisition is the in-memory record of one successful acquisition.
//
// NOT safe for concurrent use across goroutines. The omnibus runner
// owns one acquisition per dispatched run and threads the same
// `*acquisition` (or an `acquisition` value) sequentially through
// dispatch → terminal → release on a single goroutine. Release-path
// helpers (`releaseInheritedClaimsInTx`, `releaseClaim`, etc.) read
// fields like `HeldSubgraphs` / `NodeType` / `NodeID` / `InstanceID`
// without locking; if a future refactor parallelises any of those
// paths it must add explicit synchronization (or pass copies of the
// fields it needs) before sharing the pointer between goroutines.
//
// Per blessed-invariant 11 (userdata is opaque) and the cold-read
// "Explicit Code" rule, no helper here mutates `acquisition` fields
// after the acquisition tx commits — fields are effectively immutable
// post-acquisition. New fields added here must preserve that property
// or the single-goroutine contract above; mutable post-acquisition
// state belongs in a per-call value, not on `acquisition`.
type acquisition struct {
	DispatchID     shared.UUID
	NodeID         shared.UUID
	InstanceID     shared.UUID
	NodeType       string
	Executor       string
	FrameID        shared.UUID
	Locks          []AcquiredLock
	NodeDef        *node.TemplateNodeDef
	HeldSubgraphs  []node.HoldingSubgraph
	InstanceParams map[string]any
	// InstanceUserdataOverrides is the per-instance override blob loaded
	// from rimsky_instances.userdata_overrides at acquisition time.
	// Shape (validated at create-time by control-api):
	//   {"by_executor": {<name>: {<userdata-fragment>}},
	//    "by_node":     {<name>: {<userdata-fragment>}}}
	// Empty / missing → no overrides; the dispatch path's merge is a no-op
	// in that case. Per @blessed-invariant 11 the fragment values are
	// opaque to rimsky.
	InstanceUserdataOverrides map[string]any

	// PartialLocks are locks that successfully Open'd before an
	// Unavailable was encountered. Captured only when the acquisition
	// path took the Unavailable branch (errAcquireUnavailable from
	// tryAcquire); the outer caller uses these for Abandon cleanup
	// under on_acquire_unavailable resolutions of pass / error.
	PartialLocks []AcquiredLock

	// Resume is set when this acquisition resumed a parked node — the
	// dispatch path attaches a ResumeContext to the ExecuteRequest the
	// executor receives. Nil means "fresh dispatch."
	Resume *resumeMetadata
	// UnavailableSpec is the spec whose Open returned Unavailable, when
	// the acquisition took the Unavailable branch. Carried through the
	// rollback so the unavailable-handler dispatch can log / route on
	// it.
	UnavailableSpec locks.ClaimSpec

	// SubClaims is the per-sub-scope sub-claim list returned by
	// `AcquireSubClaims` when the template node declares `fan_out:`.
	// Empty for non-fan-out nodes. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Fan-out template DSL + §Recursive handler chain. The
	// fan-out leaf-dispatch path (E7) consumes this list to dispatch
	// one child run per `PartitionKey`.
	SubClaims []SubClaim

	// HeldClaims carries the per-alias claim addresses for upstream
	// claims this run co-holds (`holds:`) or inherits (legacy
	// `inherits:`). Populated at acquire-time alongside the
	// `rimsky_claim_holders` INSERTs; consumed at dispatch-time by
	// `buildStoreHandles` so the leaf's `ExecuteRequest.stores` map
	// presents co-held addresses under their local alias the same way
	// as `claims:`-acquired addresses. Per `@blessed-invariant 20`
	// rimsky carries the bytes verbatim.
	//
	// @concept: claim-co-holdership
	HeldClaims map[string]locks.ClaimResult
}

// openResult discriminates the three outcomes of acquiring one
// lock-or-claim spec under the §7.3 acquisition flow:
//
//	openResultAcquired   — the spec acquired successfully.
//	openResultUnavailable — the producer returned Available=false.
//	                        Routed through on_acquire_unavailable.
//	openResultBail        — any other reason (eligibility, scope
//	                        conflict, named-lock counter limit). The
//	                        per-candidate tx rolls back without firing
//	                        the unavailable handler.
type openResult int

const (
	openResultAcquired openResult = iota
	openResultUnavailable
	openResultBail
)

// errAcquireUnavailable is a sentinel returned from tryAcquire when
// any required claim's Open returned Unavailable. Like
// errTryAcquireRollback it's not a real error to surface — the outer
// caller interprets it and dispatches the
// on_acquire_unavailable handler.
var errAcquireUnavailable = fmt.Errorf("supervisor: acquire bailed on Unavailable claim (sentinel)")

// acquireCandidate runs the §7.3 flow against the live database.
func acquireCandidate(ctx context.Context, args RunArgs, heartbeatInterval time.Duration) (acquisition, bool, error) {
	candidates, err := selectCandidatesShortTx(ctx, args)
	if err != nil {
		return acquisition{}, false, err
	}
	if len(candidates) == 0 {
		return acquisition{}, false, nil
	}

	for _, cand := range candidates {
		if cand.FrameID == (shared.UUID{}) {
			args.Logger.Warn("acquireCandidate: skipping candidate with nil frame_id",
				"dispatch_id", cand.DispatchID.String(),
				"node_id", cand.NodeID.String(),
				"reason", "frame_id_null")
			continue
		}
		acq, ok, err := tryAcquireWithTx(ctx, args, cand, heartbeatInterval)
		if err == errAcquireUnavailable {
			handleAcquireUnavailable(ctx, args, acq, cand)
			continue
		}
		if err != nil {
			return acquisition{}, false, err
		}
		if !ok {
			continue
		}
		if !verifyBeforeRun(ctx, args, acq) {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		if err := transitionToRunning(ctx, args, acq); err != nil {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := args.Persist.Nodes().UpdateHeartbeat(ctx, acq.NodeID, args.Clock.Now(), args.SupervisorID, tx); err != nil {
				return err
			}
			if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: "work_started", Payload: map[string]any{
					"supervisor_id": args.SupervisorID,
					"dispatch_id":   acq.DispatchID.String(),
				},
			}, tx); err != nil {
				return err
			}
			for _, lk := range acq.Locks {
				if err := emitLockAcquired(ctx, args, tx, acq, lk); err != nil {
					return err
				}
			}
			return nil
		})
		return acq, true, nil
	}
	return acquisition{}, false, nil
}

// selectCandidatesShortTx runs the candidate-selection helper in its
// own short read tx. Read-only — the surrounding tx commits with no
// writes; the rows' FOR UPDATE SKIP LOCKED locks release at commit.
func selectCandidatesShortTx(ctx context.Context, args RunArgs) ([]persistence.Candidate, error) {
	limit := args.SelectCandidatesLimit
	if limit <= 0 {
		limit = 8
	}
	var candidates []persistence.Candidate
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := args.Queue.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: args.AcceptedExecutors,
			AcceptedStores:    args.AcceptedStores,
			Limit:             limit,
		})
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

// tryAcquireWithTx wraps tryAcquire in its own tx so a failed
// candidate's partial mutations roll back rather than leak into the
// next candidate. The errAcquireUnavailable sentinel propagates out so
// the outer dispatch loop can run the on_acquire_unavailable handler;
// the partial-acquired list is in acq.PartialLocks for Abandon
// cleanup.
func tryAcquireWithTx(
	ctx context.Context, args RunArgs, cand persistence.Candidate,
	heartbeatInterval time.Duration,
) (acquisition, bool, error) {
	var (
		acq acquisition
		ok  bool
	)
	err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var inner error
		acq, ok, inner = tryAcquire(ctx, args, tx, cand, heartbeatInterval)
		if inner != nil {
			return inner
		}
		if !ok {
			// Roll back so partial mutations from a non-eligible
			// candidate don't leak.
			return errTryAcquireRollback
		}
		return nil
	})
	if err == errAcquireUnavailable {
		// Tx rolled back via the sentinel. acq carries PartialLocks /
		// UnavailableSpec for the outer caller to dispatch the
		// on_acquire_unavailable handler.
		return acq, false, errAcquireUnavailable
	}
	if err != nil && err != errTryAcquireRollback {
		return acquisition{}, false, fmt.Errorf("tryAcquireWithTx: %w", err)
	}
	if err == errTryAcquireRollback {
		return acquisition{}, false, nil
	}
	return acq, ok, nil
}

// errTryAcquireRollback is a sentinel returned from the per-candidate
// acquisition tx to force a clean rollback without surfacing a real
// error to the caller. Used when in-Go eligibility checks bail before
// any state mutation should commit.
var errTryAcquireRollback = fmt.Errorf("supervisor: tryAcquire rollback (sentinel)")

// tryAcquire runs the acquisition steps for a single candidate inside
// the open rimsky-side tx. Note: ClaimProducer.Open RPCs over the wire and the
// store runs in its own tx (per spec §7.3).
//
// All persistence calls reuse the open `tx`. Passing nil here would
// self-deadlock against the SQLite driver's single-connection pool:
// the caller's tx holds the only conn, so a fresh-conn read would
// block forever waiting for the tx to commit (which can't, because
// it's awaiting the read).
func tryAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	cand persistence.Candidate, heartbeatInterval time.Duration,
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
		// Per the Per-instance-userdata-overrides feature: instance row
		// is load-bearing for dispatch (template lookup AND override
		// blob). Surface the error to the caller (mirrors the Nodes().Get
		// path above) so a sustained DB issue produces a visible signal
		// rather than a silent log-spammy skip on every candidate. The
		// outer dispatch loop logs and aborts the tick; the candidate's
		// dispatch row remains in `pending` (we bailed before
		// ClaimDispatchRow) and will be re-selected on the next tick.
		return acquisition{}, false, fmt.Errorf("tryAcquire: instances.Get: %w", err)
	}
	tmpl := lookupTemplate(ctx, args, tx, inst)
	nodeDef := lookupNodeDef(tmpl, nd.NodeType)
	specs, err := buildLockSpecs(ctx, args, tx, nd, nodeDef, inst)
	if err != nil {
		args.Logger.Warn("tryAcquire: lock-spec substitution failed",
			"node_id", cand.NodeID.String(), "error", err.Error())
		return acquisition{}, false, nil
	}
	sortLockSpecs(specs)

	if err := takeNamedAdvisoryLocks(ctx, args, tx, specs); err != nil {
		return acquisition{}, false, err
	}

	claimed, err := args.Queue.ClaimDispatchRow(ctx, tx, cand.DispatchID, args.SupervisorID)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: ClaimDispatchRow: %w", err)
	}
	if !claimed {
		return acquisition{}, false, nil
	}

	heldSubgraphs := node.HoldingSubgraphsForTemplate(tmpl)

	acquiredLocks := make([]AcquiredLock, 0, len(specs))
	for _, sp := range specs {
		al, res, err := acquireOneLock(ctx, args, tx, sp, cand, heartbeatInterval, heldSubgraphs)
		if err != nil {
			return acquisition{}, false, err
		}
		switch res {
		case openResultAcquired:
			acquiredLocks = append(acquiredLocks, al)
		case openResultUnavailable:
			// Carry the partial-acquired list and the unavailable spec
			// out across the rollback so the outer caller can dispatch
			// the on_acquire_unavailable handler.
			unavailableSpec, _ := sp.(locks.ClaimSpec)
			out := acquisition{
				DispatchID:      cand.DispatchID,
				NodeID:          cand.NodeID,
				InstanceID:      nd.InstanceID,
				NodeType:        nd.NodeType,
				Executor:        nd.Executor,
				FrameID:         cand.FrameID,
				NodeDef:         nodeDef,
				HeldSubgraphs:   heldSubgraphs,
				PartialLocks:    acquiredLocks,
				UnavailableSpec: unavailableSpec,
			}
			if inst != nil {
				out.InstanceParams = inst.Params
				out.InstanceUserdataOverrides = inst.UserdataOverrides
			}
			return out, false, errAcquireUnavailable
		case openResultBail:
			return acquisition{}, false, nil
		}
	}

	out := acquisition{
		DispatchID:    cand.DispatchID,
		NodeID:        cand.NodeID,
		InstanceID:    nd.InstanceID,
		NodeType:      nd.NodeType,
		Executor:      nd.Executor,
		FrameID:       cand.FrameID,
		Locks:         acquiredLocks,
		NodeDef:       nodeDef,
		HeldSubgraphs: heldSubgraphs,
	}
	if inst != nil {
		out.InstanceParams = inst.Params
		out.InstanceUserdataOverrides = inst.UserdataOverrides
	}
	// E4 atomic acquisition for sub-claims: when the template node
	// declares `fan_out:`, split the parent claim's scope into sub-scopes
	// inside the same transaction. The sub-claims persist with
	// `parent_claim_handle_id` pointing at the parent so the recursive
	// auto-terminal (E3) resolves bottom-up correctly. Failure aborts
	// the whole acquisition.
	//
	// @concept: fan-out
	// @concept: claim-tree
	if nodeDef != nil && nodeDef.FanOut != nil {
		// Locate the acquiredLocks entry whose Alias matches the
		// FanOut.Claim reference. The validator (D4) rejects fan_out blocks
		// that reference an unknown alias, so this lookup is best-effort
		// safe at runtime.
		fanOutClaim := nodeDef.FanOut.Claim
		var parent *AcquiredLock
		for i := range acquiredLocks {
			if acquiredLocks[i].Alias == fanOutClaim {
				parent = &acquiredLocks[i]
				break
			}
		}
		if parent != nil {
			// `parent.Spec` is `any` — narrow to ClaimSpec; named locks
			// can't be fan-out targets (no producer name).
			parentClaimSpec, ok := parent.Spec.(locks.ClaimSpec)
			if !ok {
				args.Logger.Warn("tryAcquire: fan-out alias references non-claim spec; ignored",
					"node_id", cand.NodeID.String(),
					"alias", fanOutClaim)
			} else {
				frameID := cand.FrameID
				// Substitute partition_request with the runtime-resolved
				// trigger payload. Pre-v1: the trigger-message wiring (E14)
				// passes substitution through at dispatch time; until the
				// substitution-aware caller lands, the literal bytes of the
				// canonicalized partition_request flow through verbatim.
				subClaims, err := AcquireSubClaims(ctx, args, tx, AcquireSubClaimsInput{
					ParentClaimHandleID: parent.ClaimHandleID,
					ParentScope:         parent.ClaimResult.Scope,
					ProducerName:        parentClaimSpec.ProducerName,
					NodeRunID:           cand.DispatchID,
					HolderNodeID:        cand.NodeID,
					HolderSupervisorID:  args.SupervisorID,
					FrameID:             &frameID,
					HeartbeatInterval:   heartbeatInterval,
					PartitionRequest:    []byte(nodeDef.FanOut.PartitionRequest),
					// Sub-claims inherit the parent's is_held so the rows
					// survive the leaf's active terminal until the
					// parent's recursive resolution walks them. Without
					// this, non-held sub-claim rows drop at active
					// terminal and the parent's aggregation sees an
					// empty children set, Committing prematurely.
					ParentIsHeld: parent.IsHeld,
					// AggregationPolicy is snapshotted onto the parent
					// claim handle so the recursive walker computes a
					// true aggregate Commit/Abandon decision over all
					// children's outcomes (cycle 4 issue C).
					AggregationPolicy: nodeDef.FanOut.ErrorPolicy,
				})
				if err != nil {
					args.Logger.Warn("tryAcquire: fan-out sub-claim acquisition failed",
						"node_id", cand.NodeID.String(),
						"producer", parentClaimSpec.ProducerName,
						"error", err.Error())
					return acquisition{}, false, err
				}
				out.SubClaims = subClaims
			}
		}
	}
	// E4b co-holder / inheritor row registration. For each `holds:`
	// declaration on this node, find the upstream's claim handle and
	// INSERT a `rimsky_claim_holders` row with `holder_run_id = this run`.
	// Same pattern for legacy `inherits:` entries (the inheritor's
	// dispatch-time INSERT replaces the eager acquire-time INSERTs the
	// previous model used). Done inside the acquisition tx for atomicity
	// per plan E4b step 2 — a co-holder run is either fully bound (own
	// claims acquired AND co-held claims registered) or not bound at all.
	//
	// @blessed-invariant 13: the holders set is the auto-terminal's input.
	// @concept: claim-co-holdership
	if err := insertCoHolderClaimHoldersAtAcquire(ctx, args, tx, cand, nodeDef, tmpl); err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: co-holder rows: %w", err)
	}
	// Bind co-held addresses into the dispatch-time `ExecuteRequest`:
	// load the upstream claim handles' addresses per alias and stash on
	// the acquisition. Same lookup the substitution context uses
	// (loadInheritedClaimsForNode); read once and reuse.
	if held := loadInheritedClaimsForNode(ctx, args, tx, nd); len(held) > 0 {
		out.HeldClaims = held
	}

	// Resume detection: if the node-run row carries parked metadata
	// surviving from a prior park, build a resumeMetadata struct and
	// resolve any spilled payload through the BlobBackend. The resumed
	// flag is consumed by buildExecuteRequest to populate
	// ExecuteRequest.resume_context.
	if rm, rerr := args.Queue.LoadResumeMetadataInTx(ctx, tx, cand.DispatchID); rerr == nil && rm != nil {
		payload := rm.PayloadInline
		if rm.PayloadHandle != "" {
			switch {
			case args.Blob == nil:
				args.Logger.Warn("tryAcquire: spilled resume payload but no BlobBackend configured; passing empty payload to executor",
					"node_id", cand.NodeID.String(),
					"handle_backend", rm.PayloadHandleBackend)
				payload = nil
			case args.Blob.Name() != rm.PayloadHandleBackend:
				args.Logger.Warn("tryAcquire: blob backend mismatch on resume; passing empty payload to executor",
					"node_id", cand.NodeID.String(),
					"current_backend", args.Blob.Name(),
					"handle_backend", rm.PayloadHandleBackend)
				payload = nil
			default:
				if b, berr := args.Blob.Read(ctx, persistence.Handle(rm.PayloadHandle)); berr == nil {
					payload = b
				} else {
					args.Logger.Warn("tryAcquire: blob fetch for resume payload failed; passing empty payload to executor",
						"node_id", cand.NodeID.String(), "error", berr.Error())
					payload = nil
				}
			}
		}
		// resume_reason is read from the persisted wake_reason column,
		// populated by ResumeParkedInTx at wake time. Empty wake_reason
		// (NULL) falls back to external_invalidate — covers older rows
		// upgraded in place pre-v1 and any wake path that forgot to set
		// it (none today; the fallback is defensive).
		wakeReason := WakeExternalInvalidate
		if rm.WakeReason != "" {
			wakeReason = WakeReason(rm.WakeReason)
		}
		out.Resume = &resumeMetadata{
			Payload:      payload,
			SessionToken: rm.SessionToken,
			Reason:       wakeReason,
		}
		// Observe parked duration on resume — measured from when the
		// node-run entered phase='parked' (rm.ParkedAt) to now.
		// Skipped when ParkedAt is zero (legacy rows or callers that
		// haven't backfilled the field).
		if !rm.ParkedAt.IsZero() {
			metricsOf(args).ObserveParkedDurationOnResume(args.Clock.Now().Sub(rm.ParkedAt).Seconds())
		}
	}
	return out, true, nil
}

// takeNamedAdvisoryLocks walks the sorted spec slice and takes one
// advisory lock per NamedLockSpec.
func takeNamedAdvisoryLocks(ctx context.Context, args RunArgs, tx persistence.Tx, specs []any) error {
	for _, sp := range specs {
		named, ok := sp.(locks.NamedLockSpec)
		if !ok {
			continue
		}
		if err := args.AdvisoryLocker.TakeNamedLockInTx(ctx, tx, named.Name); err != nil {
			return fmt.Errorf("takeNamedAdvisoryLocks(%q): %w", named.Name, err)
		}
	}
	return nil
}

// acquireOneLock handles one spec inside the acquisition tx.
// acquireOneLock dispatches one spec to the right acquisition path and
// returns one of the three openResult flavors. NamedLockSpec acquisitions
// never report Unavailable (acquired or bail only). ClaimSpec
// acquisitions may report Unavailable when the producer's Open returns
// Available=false.
func acquireOneLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	sp any, cand persistence.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, openResult, error) {
	switch spec := sp.(type) {
	case locks.NamedLockSpec:
		al, ok, err := acquireNamedLock(ctx, args, tx, spec, cand, heartbeatInterval)
		if err != nil {
			return AcquiredLock{}, openResultBail, err
		}
		if !ok {
			return AcquiredLock{}, openResultBail, nil
		}
		return al, openResultAcquired, nil
	case locks.ClaimSpec:
		return acquireClaim(ctx, args, tx, spec, cand, heartbeatInterval, heldSubgraphs)
	}
	return AcquiredLock{}, openResultBail, fmt.Errorf("acquireOneLock: unknown spec kind %T", sp)
}

// acquireNamedLock enforces the counter-semaphore limit then inserts
// the named lock-holder row. The per-name advisory lock has been
// taken upstream (takeNamedAdvisoryLocks); under that lock the
// CountByNamedLock + Insert pair is atomic against the limit.
//
// When the operator's NamedLocks config has no entry for this name,
// no limit is enforced (limit defaults to ∞). Templates referencing
// undeclared names should have failed validation at deploy time
// (control-api wires NamedLockDeclared unconditionally).
func acquireNamedLock(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.NamedLockSpec, cand persistence.Candidate, heartbeatInterval time.Duration,
) (AcquiredLock, bool, error) {
	if cfg, ok := args.NamedLocks.Get(spec.Name); ok {
		count, err := args.ClaimHandles.CountByNamedLock(ctx, spec.Name, tx)
		if err != nil {
			return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: CountByNamedLock(%q): %w", spec.Name, err)
		}
		if count >= cfg.Limit {
			return AcquiredLock{}, false, nil
		}
	}
	rowID := uuid.New()
	frameID := cand.FrameID
	dispatchID := cand.DispatchID
	nameCopy := spec.Name
	in := persistence.ClaimHandleInsertInput{
		ID:                 rowID,
		NodeRunID:          &dispatchID,
		LockKind:           persistence.LockKindNamed,
		LockName:           &nameCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ExpiresAt:          args.Clock.Now().Add(5 * heartbeatInterval),
		FrameID:            &frameID,
		// Named locks are never held past active terminal; they release
		// at the node-run's active-phase terminal.
		IsHeld: false,
	}
	if err := args.ClaimHandles.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, false, fmt.Errorf("acquireNamedLock: Insert: %w", err)
	}
	return AcquiredLock{
		Spec:          spec,
		ClaimHandleID: rowID,
	}, true, nil
}

// acquireClaim runs the claim-acquisition steps per spec §7.3 step 4.
//
// Conflict detection uses byte-equal comparison on scope bytes (per
// spec §7.7); the candidate's pre-Open scope is the substituted-
// selector bytes. For pick-policy claims the store's
// FOR UPDATE SKIP LOCKED prevents two supervisors picking the same
// item independently of rimsky's predicate. For scoped claims
// rimsky's predicate is the source of truth for invariant 4b.
//
// To prevent two supervisors from concurrently passing the in-Go
// scope-conflict predicate against each other's uncommitted INSERTs
// (READ COMMITTED hides them), this function takes a per-(producer_name,
// scope_data) transactional advisory lock before evaluateScopeConflict
// runs. Analogous to the named-lock advisory; under the same lock the
// list-then-INSERT pair is atomic against any concurrent acquirer
// targeting the same (producer, scope) pair.
func acquireClaim(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.ClaimSpec, cand persistence.Candidate, heartbeatInterval time.Duration,
	heldSubgraphs []node.HoldingSubgraph,
) (AcquiredLock, openResult, error) {
	// Latency timer for `rimsky_claim_acquisition_latency_seconds`. Start
	// the clock at the top of the function so the histogram includes the
	// pre-Open advisory-lock + scope-conflict check; observe only on
	// resolved outcomes (acquired / unavailable).
	acquireStart := args.Clock.Now()
	s, ok := args.StoreRegistry.Get(spec.ProducerName)
	if !ok {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: unknown store %q", spec.ProducerName)
	}
	scopeInitial, err := json.Marshal(spec.Selector)
	if err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: marshal selector: %w", err)
	}
	if err := args.AdvisoryLocker.TakeScopeLockInTx(ctx, tx, spec.ProducerName, scopeInitial); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: TakeScopeLockInTx: %w", err)
	}
	// Pre-Open conflict check: any existing scope-byte-equal holder must
	// permit our intent under its own RealizedWriteSemantics. Per the
	// uniformity invariant (spec §2.5), all byte-equal-scope claims share
	// identical semantics, so the candidate's effective semantics on a
	// match equals the holder's recorded value.
	conflicted, err := evaluateScopeConflict(ctx, args, tx, spec, cand)
	if err != nil {
		return AcquiredLock{}, openResultBail, err
	}
	if conflicted {
		return AcquiredLock{}, openResultBail, nil
	}

	rowID := uuid.New()
	frameID := cand.FrameID
	dispatchID := cand.DispatchID
	producerNameCopy := spec.ProducerName
	intentCopy := string(spec.Intent)
	// is_held is determined by the holding-subgraph membership for this
	// (acquirerType, alias). When the alias declares a held subgraph of
	// size > 1, the claim_handle persists past active terminal until
	// auto-terminal resolution.
	subgraph, hasSubgraph := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, spec.Alias)
	isHeld := hasSubgraph && subgraph.IsHeld()
	in := persistence.ClaimHandleInsertInput{
		ID:                 rowID,
		NodeRunID:          &dispatchID,
		LockKind:           persistence.LockKindScope,
		ProducerName:       &producerNameCopy,
		ScopeData:          scopeInitial,
		Intent:             &intentCopy,
		HolderSupervisorID: args.SupervisorID,
		HolderNodeID:       cand.NodeID,
		ExpiresAt:          args.Clock.Now().Add(5 * heartbeatInterval),
		FrameID:            &frameID,
		IsHeld:             isHeld,
	}
	if err := args.ClaimHandles.Insert(ctx, in, tx); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: Insert: %w", err)
	}

	claimID := locks.ClaimID(rowID.String())
	outcome, err := s.Open(ctx, claimID, spec)
	if err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: Open(%s): %w", spec.ProducerName, err)
	}
	// Producer has nothing to give right now (e.g. drained items-table
	// queue). The producer signals this via OpenOutcome.Available=false.
	// Distinguished from openResultBail so the outer caller can route
	// through the on_acquire_unavailable handler (default = silent
	// retry preserving today's behavior).
	if !outcome.Available {
		// Producer signalled unavailable — count as a resolved
		// acquisition outcome with intent="unavailable".
		metricsOf(args).IncClaimAcquisition(spec.ProducerName, "unavailable")
		metricsOf(args).ObserveClaimAcquisitionLatency(spec.ProducerName, args.Clock.Now().Sub(acquireStart).Seconds())
		return AcquiredLock{}, openResultUnavailable, nil
	}
	cr := outcome.Result

	if err := args.ClaimHandles.UpdateAddress(ctx, rowID, args.SupervisorID, cr.Address, tx); err != nil {
		return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateAddress: %w", err)
	}
	// Pick-policy claims have store-chosen scope; scoped claims
	// keep the substituted selector (already written above).
	if len(cr.Scope) > 0 && string(cr.Scope) != string(scopeInitial) {
		if err := args.ClaimHandles.UpdateScope(ctx, rowID, args.SupervisorID, cr.Scope, tx); err != nil {
			return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateScope: %w", err)
		}
	}
	// Persist the per-claim RealizedWriteSemantics returned by the
	// producer. Required for the in-Go scope-conflict check on
	// subsequent acquisitions; per the uniformity invariant (§2.5) all
	// byte-equal-Scope claims must share this value.
	if cr.RealizedWriteSemantics != "" {
		if err := args.ClaimHandles.UpdateRealizedWriteSemantics(ctx, rowID, args.SupervisorID, string(cr.RealizedWriteSemantics), tx); err != nil {
			return AcquiredLock{}, openResultBail, fmt.Errorf("acquireClaim: UpdateRealizedWriteSemantics: %w", err)
		}
	}

	if err := insertHeldClaimHoldersAtAcquire(ctx, args, tx, rowID, cand, spec.Alias, heldSubgraphs); err != nil {
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

// evaluateScopeConflict re-loads existing scope holders for the
// store and runs ScopesByteEqual ∧ ModeCoexists against the candidate
// spec. Skips own-node rows. Returns true if any holder conflicts AND
// the modes don't coexist.
//
// Per spec §7.7: byte-equal comparison; the producer canonicalizes its
// scope bytes such that two claims that should conflict produce
// byte-equal scopes. The candidate's pre-Open scope is the
// substituted-selector bytes (scoped claims) — for pick-policy
// claims the actual collision check happens in the producer's own
// internal serialization.
//
// Per the uniformity invariant (spec §2.5) all byte-equal-Scope
// claims share identical RealizedWriteSemantics. The conflict check
// uses the holder's recorded RealizedWriteSemantics for both sides.
func evaluateScopeConflict(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	spec locks.ClaimSpec, cand persistence.Candidate,
) (bool, error) {
	holders, err := args.ClaimHandles.ListByProducerScope(ctx, spec.ProducerName, tx)
	if err != nil {
		return false, fmt.Errorf("evaluateScopeConflict: ListByProducerScope: %w", err)
	}
	candidateScope, err := json.Marshal(spec.Selector)
	if err != nil {
		return false, err
	}
	for _, h := range holders {
		if h.HolderNodeID == cand.NodeID && h.HolderSupervisorID == args.SupervisorID {
			continue
		}
		if !locks.ScopesByteEqual(candidateScope, h.ScopeData) {
			continue
		}
		var holderIntent locks.Intent
		if h.Intent != nil {
			holderIntent = locks.Intent(*h.Intent)
		}
		holderRWS := locks.WriteSemantics(h.RealizedWriteSemantics)
		// By the uniformity invariant the candidate's realized semantics
		// (post-Open) MUST match the holder's; we use the holder's
		// recorded value for both sides of the matrix.
		if !locks.ModeCoexists(spec.Intent, holderRWS, holderIntent, holderRWS) {
			return true, nil
		}
	}
	return false, nil
}

// insertHeldClaimHoldersAtAcquire inserts the acquirer's own
// `rimsky_claim_holders` row when the alias is held. Post-stage-5 of
// the run-row lifecycle cutover, holder rows are keyed by
// `holder_run_id` (a `rimsky_node_runs.id`), so only the acquirer's
// own row — whose run id is known at acquire-time — is inserted here.
// Inheritor / co-holder rows are inserted at the inheritor's own
// dispatch time (see
// `runner_dispatch.go::insertCoHolderClaimHoldersAtDispatch`), where
// the inheritor's run id is the in-flight dispatch row.
//
// Inserting the acquirer's row at acquire prevents auto-terminal from
// firing prematurely before any inheritor / co-holder gets a chance to
// register: the row stays `active` until the acquirer's release path
// marks it (the `releaseClaim` held branch calls `markClaimHolderForRun`
// before `CheckAndFireResolution`).
//
// `@blessed-invariant 13`: held-claim resolution is auto-terminal,
// single, and aggregate-outcome-driven. The holders set this function
// seeds is the auto-terminal's input.
func insertHeldClaimHoldersAtAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	claimHandleID shared.UUID, cand persistence.Candidate, alias string,
	heldSubgraphs []node.HoldingSubgraph,
) error {
	subgraph, ok := findHoldingSubgraphForAcquirer(heldSubgraphs, cand.NodeType, alias)
	if !ok || !subgraph.IsHeld() {
		return nil
	}
	frameID := cand.FrameID
	if err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
		ID:            uuid.New(),
		ClaimHandleID: claimHandleID,
		HolderRunID:   cand.DispatchID,
		FrameID:       &frameID,
	}, tx); err != nil {
		return fmt.Errorf("insertHeldClaimHoldersAtAcquire: Insert: %w", err)
	}
	return nil
}

// insertCoHolderClaimHoldersAtAcquire inserts one `rimsky_claim_holders`
// row per co-holdership declared by this node. Two sources:
//
//  1. `holds:` — explicit co-holdership (spec §Claim co-holdership).
//     Each entry names an upstream node-alias whose claim is co-held.
//  2. `inherits:` — legacy pre-co-holdership inheritance. The acquirer
//     is resolved via the holding-subgraph computation.
//
// The row's `holder_run_id` is this run's id (`cand.DispatchID`);
// `state` is `'active'`. Idempotent — duplicate inserts in the same
// tx are blocked by the table's UNIQUE (claim_handle_id, holder_run_id).
//
// Runs inside the caller's tx (the acquisition tx). Per plan E4b step 2,
// the INSERTs commit atomically with this run's own claim acquisition.
//
// @blessed-invariant 13
// @concept: claim-co-holdership
func insertCoHolderClaimHoldersAtAcquire(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	cand persistence.Candidate, nodeDef *node.TemplateNodeDef, tmpl *node.TemplateSpec,
) error {
	if nodeDef == nil || tmpl == nil {
		return nil
	}
	if len(nodeDef.Holds) == 0 && len(nodeDef.Inherits) == 0 {
		return nil
	}
	nd, err := args.Persist.Nodes().Get(ctx, cand.NodeID, tx)
	if err != nil || nd == nil {
		return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: nodes.Get: %w", err)
	}
	frameID := cand.FrameID
	// `holds:` entries (post-co-holdership wiring).
	for alias, binding := range nodeDef.Holds {
		upstreamType := binding.From
		if upstreamType == "" {
			continue
		}
		upstreamNode := findInstanceNodeByType(ctx, args, tx, nd.InstanceID, upstreamType)
		if upstreamNode == nil {
			args.Logger.Warn("insertCoHolderClaimHoldersAtAcquire: upstream node-type not found in instance",
				"node_id", cand.NodeID.String(),
				"alias", alias,
				"upstream_type", upstreamType)
			continue
		}
		lh := lookupClaimHandleForAlias(ctx, args, tx, upstreamNode.ID, tmpl, upstreamType, alias)
		if lh == nil {
			// Upstream's claim handle is missing — either the upstream
			// hasn't acquired yet (DAG violation: holds.from must be an
			// upstream dependency), or auto-terminal already fired
			// (held_durable=false claim deleted). Skip silently;
			// CheckAndFireResolution is idempotent.
			continue
		}
		if err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:            uuid.New(),
			ClaimHandleID: lh.ID,
			HolderRunID:   cand.DispatchID,
			FrameID:       &frameID,
		}, tx); err != nil {
			return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: holds: %w", err)
		}
	}
	// `inherits:` entries (legacy). Each names an alias; the acquirer is
	// the unique upstream member that acquires the alias per the
	// holding-subgraph computation.
	if len(nodeDef.Inherits) > 0 {
		subgraphs := node.HoldingSubgraphsForTemplate(tmpl)
		for _, ie := range nodeDef.Inherits {
			alias := ie.Claim
			if alias == "" {
				continue
			}
			var acquirerType string
			for _, sg := range subgraphs {
				if sg.Alias != alias {
					continue
				}
				if !memberOf(sg, nodeDef.Type) {
					continue
				}
				if sg.AcquirerType == nodeDef.Type {
					continue
				}
				acquirerType = sg.AcquirerType
				break
			}
			if acquirerType == "" {
				continue
			}
			upstreamNode := findInstanceNodeByType(ctx, args, tx, nd.InstanceID, acquirerType)
			if upstreamNode == nil {
				continue
			}
			lh := lookupClaimHandleForAlias(ctx, args, tx, upstreamNode.ID, tmpl, acquirerType, alias)
			if lh == nil {
				continue
			}
			if err := args.Persist.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
				ID:            uuid.New(),
				ClaimHandleID: lh.ID,
				HolderRunID:   cand.DispatchID,
				FrameID:       &frameID,
			}, tx); err != nil {
				return fmt.Errorf("insertCoHolderClaimHoldersAtAcquire: inherits: %w", err)
			}
		}
	}
	return nil
}

// findHoldingSubgraphForAcquirer locates the (acquirerType, alias)
// subgraph in the precomputed list.
func findHoldingSubgraphForAcquirer(subgraphs []node.HoldingSubgraph, acquirerType, alias string) (node.HoldingSubgraph, bool) {
	for _, sg := range subgraphs {
		if sg.AcquirerType == acquirerType && sg.Alias == alias {
			return sg, true
		}
	}
	return node.HoldingSubgraph{}, false
}

// verifyBeforeRun is the separate-read guard.
func verifyBeforeRun(ctx context.Context, args RunArgs, acq acquisition) bool {
	ownership, err := args.Queue.GetClaimedBy(ctx, acq.DispatchID)
	if err != nil {
		args.Logger.Warn("verifyBeforeRun: GetClaimedBy failed",
			"dispatch_id", acq.DispatchID.String(), "error", err.Error())
		return false
	}
	return ownership.Kind == "claimed_by" && ownership.SupervisorID == args.SupervisorID
}

// handleOrphanedClaim is the race-detection bail path: the supervisor
// has already opened the claim, inserted the lock-holder row, and
// committed the acquisition tx — and then verify-before-run discovered
// that another supervisor stole the dispatch row in the gap between
// commit and the second-read guard. The supervisor knows it just
// opened the store state and is now unwinding the in-progress
// acquisition; it owns the cleanup and calls Abandon on the store
// to release any partial state (via the shared abandonOpenedClaim
// helper — see @concept terminal-resolution), then deletes its own
// lock-holder row claimant-guarded, then emits orphaned_claim_lost_race.
//
// This is NOT the periodic orphan reaper. The periodic reaper at
// `graph/scheduler/sweep_locks.go::sweepClaimHandles` deletes expired
// lock-holder rows WITHOUT firing Abandon, per v3 spec §7.5: the
// store's own TTL/sweep handles internal state for owners that
// crashed without unwinding. The two paths are deliberately distinct:
// the bail path fires Abandon because the supervisor knows what it
// just did; the reaper does NOT fire Abandon because it can't
// distinguish a crashed-supervisor state from any other.
//
// @concept: terminal-resolution
func handleOrphanedClaim(ctx context.Context, args RunArgs, acq acquisition) {
	for _, lk := range acq.Locks {
		if lk.Producer != nil {
			scope := claimScope(lk)
			address := claimAddress(lk)
			if err := abandonOpenedClaim(ctx, lk.Producer, lk.ClaimHandleID, scope, address); err != nil {
				args.Logger.Warn("handleOrphanedClaim: Abandon failed",
					"producer", producerNameForSpec(lk.Spec), "error", err.Error())
			}
		}
		_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return args.ClaimHandles.Delete(ctx, lk.ClaimHandleID, args.SupervisorID, tx)
		})
	}
	_ = args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
			Kind: "orphaned_claim_lost_race",
			Payload: map[string]any{
				"dispatch_id":   acq.DispatchID.String(),
				"supervisor_id": args.SupervisorID,
			},
		}, tx)
	})
}

// transitionToRunning is the short-tx state transition.
func transitionToRunning(ctx context.Context, args RunArgs, acq acquisition) error {
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Nodes().UpdateState(ctx, acq.NodeID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, "", tx)
	})
}

// emitLockAcquired emits the per-spec lock_acquired event using the
// caller's open `tx`. Tx-required to prevent the nested-tx footgun
// (mirrors emitLockReleased): a fresh inner Persist.Transaction would
// self-deadlock under SQLite (MaxOpenConns=1) and tie up two pool
// connections under postgres if any future callsite invoked this from
// inside an open tx.
func emitLockAcquired(
	ctx context.Context, args RunArgs, tx persistence.Tx, acq acquisition, lk AcquiredLock,
) error {
	payload := map[string]any{
		"holder_id":     lk.ClaimHandleID.String(),
		"supervisor_id": args.SupervisorID,
	}
	switch sp := lk.Spec.(type) {
	case locks.NamedLockSpec:
		payload["lock_kind"] = string(persistence.LockKindNamed)
		payload["lock_name"] = sp.Name
	case locks.ClaimSpec:
		payload["lock_kind"] = string(persistence.LockKindScope)
		payload["producer_name"] = sp.ProducerName
		payload["alias"] = sp.Alias
		payload["intent"] = string(sp.Intent)
	}
	if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
		NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
		Kind: "lock_acquired", Payload: payload,
	}, tx); err != nil {
		return fmt.Errorf("emitLockAcquired: %w", err)
	}
	return nil
}

// claimScope returns the store's scope bytes for a ClaimSpec
// acquisition; nil for NamedLockSpec.
func claimScope(lk AcquiredLock) []byte {
	if lk.Producer == nil {
		return nil
	}
	return []byte(lk.ClaimResult.Scope)
}

// claimAddress returns the store's address bytes for a ClaimSpec
// acquisition; nil for NamedLockSpec.
func claimAddress(lk AcquiredLock) []byte {
	if lk.Producer == nil {
		return nil
	}
	return []byte(lk.ClaimResult.Address)
}
