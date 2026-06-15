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
// Two primitives, two types: locks.NamedLockSpec and claimproducer.ClaimSpec.

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

// producerErrorClassOf recovers the translated error_class from a
// faulted claim-producer Open RPC. Returns "" when err is not a
// *peer.ProducerCallError or carries no ErrorInfo-derived class.
func producerErrorClassOf(err error) string {
	var pcErr *peer.ProducerCallError
	if errors.As(err, &pcErr) {
		return pcErr.ErrorClass
	}
	return ""
}

// lookupGraphName returns the GraphSpec.Name whose Nodes contains
// nodeType. Falls back to spec.MainGraphName when no GraphSpec
// contains the type or when the template carries no sub-graphs
// (legacy flat-Nodes templates with empty Graphs list).
//
// For entry-absorbed dispatches the outer-graph resolution is
// automatic: the calling node is declared in the outer graph's
// Nodes list, so the lookup finds the outer GraphSpec. Sub-graph
// *internal* nodes are declared inside the sub-graph's Nodes list,
// so the lookup naturally returns the sub-graph name for them.
//
// @concept: attribute (L5 matcher overlay's graph-key derivation)
func lookupGraphName(graphs []spec.GraphSpec, nodeType string) string {
	for _, g := range graphs {
		for _, n := range g.Nodes {
			if n.Type == nodeType {
				return g.Name
			}
		}
	}
	return spec.MainGraphName
}

// resumeMetadata captures the parked metadata for a resumed run. When
// non-nil, the runner attaches a ResumeContext to the ExecuteRequest
// the executor receives. Per plan E4.
type resumeMetadata struct {
	// @constraint: post-spill resolution of Park.payload — bytes have
	// already been expanded from any spill reference.
	Payload      []byte
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
// Per the cold-read "Explicit Code" rule, most fields here are
// populated inside the acquisition tx and remain immutable for the
// lifetime of the acquisition. A small, documented set of fields is
// enriched at dispatch time on the same goroutine that owns the
// acquisition; today that is just `MergedAttributes` (filled by
// `resolveAttributes` in `runner_dispatch.go` once the L3 + L4
// instance overrides have been merged against the resolved attribute
// bag). The mutation is safe because the dispatch path runs serially
// after acquisition completes on the same goroutine that holds the
// acquisition value; no second goroutine ever observes the field.
//
// New fields added here must either preserve true post-acquisition
// immutability OR document a similar dispatch-time-only mutation
// window. Mutable state that would outlive the dispatch goroutine
// belongs in a per-call value, not on `acquisition`.
type acquisition struct {
	DispatchID shared.UUID
	NodeID     shared.UUID
	InstanceID shared.UUID
	NodeType   string
	Executor   string
	// GraphName is the name of the template's graph this dispatch
	// belongs to — "main" (spec.MainGraphName) for main-graph dispatches
	// and the sub-graph name for internal-sub-graph dispatches. For
	// entry-absorbed dispatches (where a sub-graph's entry node shares
	// runtime identity with the calling node per concept:delegation),
	// the outer graph wins — the row's declared template location.
	//
	// Derived at acquisition time by consulting the bound template's
	// Graphs list (spec.TemplateSpec.Graphs) and finding the GraphSpec
	// whose Nodes contains NodeType. Legacy flat-Nodes templates with
	// an empty Graphs list resolve to "main".
	//
	// Consumed by applyAttributeOverrides (L5 matcher evaluation).
	//
	// @concept: attribute (L5 matcher overlay)
	GraphName string

	// RunScopeID is the RunScope this dispatch lives in. Non-zero for
	// every acquisition. Per concept:run-scope. ChildKey (the
	// producer-emitted partition key for fan-out children) and
	// ParentRunID (the calling-run id for sub-graph / fan-out
	// children) used to be carried inline; both are now derived by
	// looking up the RunScope (see runtime/runner_acquire_scope.go).
	//
	// @concept: run-scope
	RunScopeID shared.UUID

	// PriorDispatchID, when non-nil, is the rimsky_node_runs.id of
	// the predecessor dispatch this run supersedes — populated when
	// the supervisor enqueues a recovery / retry / recalculate. Nil
	// on initial dispatches. Surfaced on
	// proto:executor.proto::ExecuteRequest.prior_dispatch_id at
	// dispatch.
	//
	// @concept: run-scope
	PriorDispatchID *shared.UUID
	// PriorDispatchDisposition classifies why PriorDispatchID is set
	// (lower_snake_case storage form: "heartbeat_stale" /
	// "retry_after_error" / "recalculate"). Empty when
	// PriorDispatchID is nil. Surfaced on
	// proto:executor.proto::ExecuteRequest.prior_dispatch_disposition.
	//
	// @concept: run-scope
	PriorDispatchDisposition string

	FrameID        shared.UUID
	Locks          []AcquiredLock
	NodeDef        *node.TemplateNodeDef
	HeldSubgraphs  []node.HoldingSubgraph
	InstanceParams map[string]any
	// InstanceAttributeOverrides is the per-instance override blob loaded
	// from rimsky_instances.attribute_overrides at acquisition time.
	// Shape (validated at create-time by control-api):
	//   {"by_executor": {<name>: {<attribute-fragment>}},
	//    "by_node":     {<name>: {<attribute-fragment>}}}
	// Empty / missing → no overrides; the dispatch path's merge is a no-op
	// in that case. Per concept:inertness the fragment values are inert
	// to rimsky.
	InstanceAttributeOverrides map[string]any

	// TemplateAttributeDefaults is the already-routed by-executor fragment
	// from the bound template's
	// `TemplateSpec.Defaults.Attributes.ByExecutor[Executor]`. Populated at
	// acquisition time from the bound template so the dispatch path does
	// not re-fetch the template. L1 in the four-layer override merge —
	// folded into the effective schema's `default:` values at dispatch
	// (see runner_dispatch.go::computeEffectiveAttributeSchema).
	//
	// @concept: attribute
	TemplateAttributeDefaults map[string]any

	// PartialLocks are locks that successfully Open'd before an
	// Unavailable was encountered. Captured only when the acquisition
	// path took the Unavailable branch (errAcquireUnavailable from
	// tryAcquire); the outer caller uses these for Abandon cleanup
	// under the `error_types: { acquire/unavailable: ... }` chain's
	// pass / give_up resolutions.
	PartialLocks []AcquiredLock

	// Resume is set when this acquisition resumed a parked node — the
	// dispatch path attaches a ResumeContext to the ExecuteRequest the
	// executor receives. Nil means "fresh dispatch."
	Resume *resumeMetadata
	// UnavailableSpec is the spec whose Open returned Unavailable, when
	// the acquisition took the Unavailable branch. Carried through the
	// rollback so the unavailable-handler dispatch can log / route on
	// it.
	UnavailableSpec claimproducer.ClaimSpec
	// UnavailableClass is the producer-declared acquisition-failure class
	// (OpenOutcome.UnavailableClass) carried out of the Unavailable
	// branch. handleAcquireUnavailable keys the operator's `error_types:`
	// chain on this producer-declared leaf (e.g. "pg/claim_unavailable")
	// when non-empty, falling back to the synthetic "acquire/unavailable"
	// when empty.
	UnavailableClass string

	// ErroredSpec is the spec whose Open RPC faulted, when the
	// acquisition took the producer-errored branch
	// (errAcquireProducerErrored). Carried through the rollback so the
	// producer-error handler can log / route on the producer name.
	ErroredSpec claimproducer.ClaimSpec
	// ProducerErrorClass is the translated error_class from the faulted
	// producer's *peer.ProducerCallError (the gRPC ErrorInfo.Reason, or
	// "" when the producer attached no ErrorInfo detail). Routed through
	// the operator's `error_types:` chain by handleAcquireProducerError.
	ProducerErrorClass string

	// SubClaims is the per-sub-scope sub-claim list returned by
	// `AcquireSubClaims` when the template node declares `fan_out:`.
	// Empty for non-fan-out nodes. Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Fan-out template DSL + §Recursive handler chain. The
	// fan-out leaf-dispatch path (E7) consumes this list to dispatch
	// one child run per `PartitionKey`.
	SubClaims []SubClaim

	// HeldClaims carries the per-alias claim addresses for upstream
	// claims this run co-holds (`holds:`). Populated at acquire-time
	// alongside the `rimsky_claim_holders` INSERTs; consumed at
	// dispatch-time by
	// `buildStoreHandles` so the leaf's `ExecuteRequest.stores` map
	// presents co-held addresses under their local alias the same way
	// as `claims:`-acquired addresses. Per `@blessed-invariant 20`
	// rimsky carries the bytes verbatim.
	//
	// @concept: claim-co-holdership
	HeldClaims map[string]claimproducer.ClaimResult

	// MergedAttributes is the per-dispatch attribute bag the executor
	// actually saw on this dispatch — the result of source-resolution +
	// static-default emission + L3 + L4 override merge. Populated by
	// `resolveAttributes` in `runner_dispatch.go` once the bag is final.
	// Consumed by the lineage writer so the per-row attribute hash
	// reflects what shipped to the executor. Per concept:inertness
	// rimsky never inspects fragment values; the hash is computed over
	// the merged shape verbatim.
	//
	// @concept: attribute
	MergedAttributes map[string]any

	// TemplateHash is the content-addressed template id
	// (`rimsky_instances.template_hash`) loaded during acquisition.
	// Threaded into `LeafRunRecord.TemplateHash` so the lineage row
	// records WHICH template version produced the run, supporting
	// time-travel queries ("what shape ran when this row was emitted?").
	// Empty when the instance row is missing (defense-in-depth; the
	// acquisition path normally aborts before reaching emit if the
	// instance lookup failed).
	TemplateHash string

	// Scratch carries the dispatch row's scratch bytes for wire-attach
	// onto the executor's ExecuteRequest. Empty on initial dispatches
	// and on dispatches whose prior row had no scratch. Spilled handles
	// are materialized via the configured BlobBackend before this field
	// populates. @concept: executor
	Scratch []byte
}

// openResult discriminates the outcomes of acquiring one lock-or-claim
// spec under the §7.3 acquisition flow:
//
//	openResultAcquired   — the spec acquired successfully.
//	openResultUnavailable — the producer returned Available=false.
//	                        Routed through the operator's
//	                        `error_types: { acquire/unavailable: ... }`
//	                        chain (synthetic class).
//	openResultErrored     — the producer's Open RPC faulted (the wire
//	                        call returned a gRPC error). The translated
//	                        error_class travels on the returned
//	                        *peer.ProducerCallError and is routed through
//	                        the operator's `error_types:` chain — the
//	                        same chain executor Error{error_class}
//	                        terminals use. Distinguished from
//	                        openResultBail so the producer fault reaches
//	                        the policy chain rather than aborting the tick.
//	openResultBail        — any other reason (eligibility, scope
//	                        conflict, named-lock counter limit). The
//	                        per-candidate tx rolls back without firing
//	                        the error-policy chain.
type openResult int

const (
	openResultAcquired openResult = iota
	openResultUnavailable
	openResultErrored
	openResultBail
)

// errAcquireUnavailable is a sentinel returned from tryAcquire when
// any required claim's Open returned Unavailable. Like
// errTryAcquireRollback it's not a real error to surface — the outer
// caller interprets it and routes through the operator's
// `error_types: { acquire/unavailable: ... }` chain.
var errAcquireUnavailable = fmt.Errorf("supervisor: acquire bailed on Unavailable claim (sentinel)")

// errAcquireProducerErrored is a sentinel returned from tryAcquire when
// a required claim's Open RPC faulted (the producer-side wire call
// returned a gRPC error). Like errAcquireUnavailable it is not a real
// error to surface — the per-candidate tx rolls back and the outer
// caller routes the translated error_class through the operator's
// `error_types:` chain (the same chain executor Error{error_class}
// terminals use). acq carries ErroredSpec + ProducerErrorClass +
// PartialLocks for the routing + Abandon cleanup.
var errAcquireProducerErrored = fmt.Errorf("supervisor: acquire bailed on producer-faulted claim (sentinel)")

// errAcquireRestampLost is a sentinel returned from tryAcquire when the
// linked sub-claim holder restamp (restampLinkedSubClaimHolders) lost
// its CAS to a concurrent supervisor — a concurrent resolution or
// competing handoff changed the holder between the read and the
// guarded UPDATE. This is an EXPECTED concurrent-supervisor race, not
// a persistence fault: the per-candidate tx rolls back (the leaf never
// runs under a supervisor that cannot settle it), and the batch loop
// logs and moves on to the NEXT candidate instead of aborting the
// whole acquisition batch. Real DB errors from the restamp still
// surface as ordinary errors and abort the batch.
var errAcquireRestampLost = fmt.Errorf("supervisor: acquire lost sub-claim restamp CAS (sentinel)")

// defaultSelectCandidatesLimit is the per-batch candidate window when
// RunArgs.SelectCandidatesLimit is unset.
const defaultSelectCandidatesLimit = 8

// acquireCandidate runs the §7.3 flow against the live database.
//
// Candidate selection pages with a keyset cursor: when a full batch
// yields no acquisition (every candidate gated or otherwise skipped),
// the next batch is selected strictly after the last (enqueued_at, id)
// pair instead of re-reading the same head — without this, ≥ Limit
// long-gated old candidates (e.g. receivers under a parked upstream)
// would hold the selection window every poll and starve younger
// ungated rows for as long as the gates hold.
func acquireCandidate(ctx context.Context, args RunArgs, heartbeatInterval time.Duration) (acquisition, bool, error) {
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
		acq, ok, err := tryAcquireBatch(ctx, args, candidates, heartbeatInterval)
		if err != nil || ok {
			return acq, ok, err
		}
		if len(candidates) < limit {
			return acquisition{}, false, nil
		}
		last := candidates[len(candidates)-1]
		cursorEnqueued, cursorID = last.EnqueuedAt, last.DispatchID
	}
}

// tryAcquireBatch walks one selected candidate batch through the §7.3
// per-candidate flow; returns the first successful acquisition.
func tryAcquireBatch(
	ctx context.Context, args RunArgs, candidates []persistence.Candidate,
	heartbeatInterval time.Duration,
) (acquisition, bool, error) {
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
		if err == errAcquireProducerErrored {
			handleAcquireProducerError(ctx, args, acq, cand)
			continue
		}
		if err == errAcquireRestampLost {
			// @deliberate: expected concurrent-supervisor race on the linked
			// sub-claim holder restamp. The candidate's tx rolled back; the
			// row stays pending and is re-selected next poll. Skip ONLY this
			// candidate — aborting the batch would cost every sibling a poll
			// cycle for a non-fault.
			args.Logger.Info("tryAcquire: sub-claim holder restamp lost CAS to concurrent supervisor; skipping candidate",
				"dispatch_id", cand.DispatchID.String(),
				"node_id", cand.NodeID.String())
			continue
		}
		if err != nil {
			return acquisition{}, false, err
		}
		if !ok {
			continue
		}
		// @deliberate: test-only seam that deterministically injects behavior
		// into the post-commit / pre-verify window so an integration test
		// can force the cross-transaction ownership flip that
		// `@blessed-invariant 5`'s verify-before-run guard catches. Nil in
		// production (no behavior change); see RunArgs.PostCommitHook.
		if args.PostCommitHook != nil {
			args.PostCommitHook(ctx)
		}
		if !verifyBeforeRun(ctx, args, acq) {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		if err := transitionToRunning(ctx, args, acq); err != nil {
			handleOrphanedClaim(ctx, args, acq)
			return acquisition{}, false, nil
		}
		// @deliberate: increment named-lock "acquired" counters post-commit,
		// not inside the per-candidate tx, so a later spec failing that tx
		// never leaves a phantom acquisition behind a rollback. Labeled with
		// the bounded template name (namedLockMetricLabel).
		for _, lk := range acq.Locks {
			if nls, isNamed := lk.Spec.(locks.NamedLockSpec); isNamed {
				metricsOf(args).IncNamedLockAcquisition(namedLockMetricLabel(nls), "acquired")
			}
		}
		// @constraint: post-acquisition audit-log tx — heartbeat refresh,
		// `work_started` event append, per-lock `lock_acquired` event
		// appends. Best-effort: the dispatch has already committed at this
		// point, so a failed audit append must not abort the dispatch.
		// WARN-and-continue so the loss is visible without losing the
		// in-flight work.
		if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := args.Persist.Nodes().UpdateHeartbeat(ctx, acq.NodeID, acq.RunScopeID, args.Clock.Now(), args.SupervisorID, tx); err != nil {
				return err
			}
			if err := args.Persist.Events().Append(ctx, persistence.EventAppendInput{
				NodeID: &acq.NodeID, InstanceID: &acq.InstanceID,
				Kind: events.KindWorkStarted(), Payload: map[string]any{
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
		}); err != nil {
			args.Logger.Warn("tryAcquire: post-acquisition audit tx failed; heartbeat/work_started/lock_acquired events lost",
				"dispatch_id", acq.DispatchID.String(),
				"node_id", acq.NodeID.String(),
				"error", err.Error())
		}
		return acq, true, nil
	}
	return acquisition{}, false, nil
}

// selectCandidatesShortTx runs the candidate-selection helper in its
// own short read tx. Read-only — the surrounding tx commits with no
// writes; the rows' FOR UPDATE SKIP LOCKED locks release at commit.
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
		out, err := args.Queue.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:          args.AcceptedExecutors,
			AcceptedStores:             args.AcceptedStores,
			Limit:                      limit,
			LateBindExecutorProxy:      args.LateBindServiceProxies["executor"],
			LateBindClaimProducerProxy: args.LateBindServiceProxies["claim_producer"],
			CursorEnqueuedAfter:        cursorEnqueued,
			CursorAfterDispatchID:      cursorID,
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
// the outer dispatch loop can route through the operator's
// `error_types: { acquire/unavailable: ... }` chain; the partial-
// acquired list is in acq.PartialLocks for Abandon cleanup.
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
			// @constraint: roll back so partial mutations from a non-eligible
			// candidate don't leak into the next candidate's tx.
			return errTryAcquireRollback
		}
		return nil
	})
	if err == errAcquireUnavailable {
		// @constraint: tx rolled back via the sentinel; acq carries
		// PartialLocks / UnavailableSpec for the outer caller to route
		// through the `error_types: { acquire/unavailable: ... }` chain.
		return acq, false, errAcquireUnavailable
	}
	if err == errAcquireProducerErrored {
		// @constraint: tx rolled back via the sentinel; acq carries
		// PartialLocks / ErroredSpec / ProducerErrorClass for the outer
		// caller to route through the operator's `error_types:` chain.
		return acq, false, errAcquireProducerErrored
	}
	if err == errAcquireRestampLost {
		// @constraint: tx rolled back via the sentinel — the sub-claim
		// holder restamp lost its CAS to a concurrent supervisor. The batch
		// loop logs and skips to the next candidate.
		return acquisition{}, false, errAcquireRestampLost
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
		// @constraint: the instance row is load-bearing for dispatch
		// (template lookup AND override blob). Surface the error to the
		// caller (mirrors the Nodes().Get path above) so a sustained DB
		// issue produces a visible signal rather than a silent log-spammy
		// skip on every candidate. The outer dispatch loop logs and aborts
		// the tick; the candidate's dispatch row remains in `pending` (we
		// bailed before ClaimDispatchRow) and will be re-selected on the
		// next tick.
		return acquisition{}, false, fmt.Errorf("tryAcquire: instances.Get: %w", err)
	}
	tmpl := lookupTemplate(ctx, args, tx, inst)
	nodeDef := lookupNodeDef(tmpl, nd.NodeType)
	templateAttributeDefaults := templateAttributeDefaultsFor(tmpl, nd.Executor)
	// @concept: attribute — derive the dispatch-time graph name for the L5
	// matcher-overlay layer. Graph name comes from the bound template's
	// Graphs list (or `spec.MainGraphName` for legacy flat-Nodes
	// templates). RunScopeID comes from the run-tree row; partition_key /
	// parent_run_id are looked up on demand via the RunScope
	// (resolveAcqScopeTuple / resolveAcqPartitionKey).
	graphName := spec.MainGraphName
	if tmpl != nil {
		graphName = lookupGraphName(tmpl.Graphs, nd.NodeType)
	}
	var runScopeID shared.UUID
	if rt := args.Persist.RunTree(); rt != nil {
		row, err := rt.GetByID(ctx, tx, cand.DispatchID)
		if err != nil {
			// @constraint: the run-scope key is load-bearing for the
			// upstream gate below (a zero scope would dispatch ungated —
			// the gate fails closed on it). Surface the error like the
			// instances.Get path above: the outer loop logs and aborts the
			// tick; the row stays pending and is re-selected.
			return acquisition{}, false, fmt.Errorf("tryAcquire: run-tree GetByID: %w", err)
		}
		if row == nil {
			// @deliberate: benign selection race — the dispatch row legally
			// retired (completed / removed by another supervisor or sweep)
			// between candidate selection and this lookup. Skip ONLY this
			// candidate — aborting the whole tick would cost every sibling
			// candidate a poll cycle for a non-fault. The fail-closed
			// posture is reserved for the lookup-errored branch above.
			args.Logger.Info("tryAcquire: run row absent (retired between selection and acquire); skipping candidate",
				"dispatch_id", cand.DispatchID.String(),
				"node_id", cand.NodeID.String())
			return acquisition{}, false, nil
		}
		runScopeID = row.RunScopeID
	}
	// @constraint: upstream-gating eligibility condition (pre-claim,
	// pre-mutation) — skip the candidate while any subscribed upstream has
	// an in-flight run in the candidate's (frame, run scope), regardless
	// of which propagation path made the receiver stale. The row stays
	// pending; the sender's settlement re-triggers selection. Sits beside
	// the other in-Go eligibility checks so it shares the candidate
	// context and the §7.3 try tx (a bail here rolls back cleanly). See
	// runner_acquire_upstream_gate.go for the invariant.
	gated, err := candidateGatedByInFlightUpstream(ctx, args, tx, nd, inst, cand, runScopeID)
	if err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: upstream gate: %w", err)
	}
	if gated {
		return acquisition{}, false, nil
	}
	specs, err := buildLockSpecs(ctx, args, tx, nd, nodeDef, inst, cand.DispatchID, cand.FrameID, runScopeID)
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
		al, res, err := acquireOneLock(ctx, args, tx, nd.InstanceID, sp, cand, heartbeatInterval, heldSubgraphs)
		if res == openResultErrored {
			// @constraint: a producer Open RPC faulted. err is the
			// *peer.ProducerCallError carrying the translated error_class;
			// recover it and carry the partial-acquired list + errored spec
			// out across the rollback so the outer caller routes the class
			// through the operator's `error_types:` chain — the same chain
			// executor Error{error_class} terminals use.
			erroredSpec, _ := sp.(claimproducer.ClaimSpec)
			out := acquisition{
				DispatchID:                cand.DispatchID,
				NodeID:                    cand.NodeID,
				InstanceID:                nd.InstanceID,
				NodeType:                  nd.NodeType,
				Executor:                  nd.Executor,
				GraphName:                 graphName,
				RunScopeID:                runScopeID,
				PriorDispatchID:           cand.PriorDispatchID,
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
			return acquisition{}, false, err
		}
		switch res {
		case openResultAcquired:
			acquiredLocks = append(acquiredLocks, al)
		case openResultUnavailable:
			// @constraint: carry the partial-acquired list and the
			// unavailable spec out across the rollback so the outer caller
			// can route through the operator's
			// `error_types: { acquire/unavailable: ... }` chain.
			unavailableSpec, _ := sp.(claimproducer.ClaimSpec)
			out := acquisition{
				DispatchID:                cand.DispatchID,
				NodeID:                    cand.NodeID,
				InstanceID:                nd.InstanceID,
				NodeType:                  nd.NodeType,
				Executor:                  nd.Executor,
				GraphName:                 graphName,
				RunScopeID:                runScopeID,
				PriorDispatchID:           cand.PriorDispatchID,
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
			return acquisition{}, false, nil
		}
	}

	out := acquisition{
		DispatchID:                cand.DispatchID,
		NodeID:                    cand.NodeID,
		InstanceID:                nd.InstanceID,
		NodeType:                  nd.NodeType,
		Executor:                  nd.Executor,
		GraphName:                 graphName,
		RunScopeID:                runScopeID,
		PriorDispatchID:           cand.PriorDispatchID,
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
	if err := acquireFanOutIfDeclared(ctx, args, tx, nd.InstanceID, &out, cand, nodeDef, acquiredLocks, heartbeatInterval); err != nil {
		return acquisition{}, false, err
	}
	// @constraint: cross-supervisor sub-claim handoff. The sub-claim rows
	// linked to this leaf run were INSERTed at the PARENT's acquisition
	// under the parent acquirer's supervisor id, but ANY replica can claim
	// the leaf run — without the restamp, the leaf's terminal would
	// resolve under a supervisor that fails every claimant guard (Promote
	// no-ops with a WARN, the parent's settlement walk stalls, and the
	// chain unwinds only via the orphan reaper's spurious Abandon AFTER
	// the producer already saw Commit). Re-stamping inside the leaf's
	// acquisition tx makes the acquiring supervisor the legitimate holder,
	// CAS-guarded from the holder observed in this same tx
	// (`@blessed-invariant 4`). NOT best-effort: a failed restamp aborts
	// this candidate's tx so the leaf never runs under a supervisor that
	// cannot settle it.
	if err := restampLinkedSubClaimHolders(ctx, args, tx, cand); err != nil {
		return acquisition{}, false, err
	}
	// @concept: fan-out — E4 leaf candidate-handle binding. A fan-out LEAF
	// (a child run in a fanout_partition RunScope) Open's a fresh
	// parent-selector claim of its own at acquisition above, but the
	// candidate handle the producer minted for THIS partition lives on the
	// linked sub-claim row (whose node_run_id was repointed to this leaf
	// in `child_execution.go::DispatchChildren`). Resolve it now and carry
	// it onto the matching AcquiredLock so `makeClaimHandle` can stamp
	// `StoreHandle.candidate_handle` at dispatch. Best-effort: a lookup
	// failure logs and leaves the candidate empty (degrades to the pre-E4
	// behaviour) rather than failing the leaf dispatch.
	bindLeafCandidateHandles(ctx, args, tx, &out, cand)
	// @concept: claim-co-holdership — E4b co-holder row registration. For
	// each `holds:` declaration on this node, find the upstream's claim
	// handle and INSERT a `rimsky_claim_holders` row with
	// `holder_run_id = this run`. The co-holder's dispatch-time INSERT
	// replaces the eager acquire-time INSERTs the previous model used.
	// Done inside the acquisition tx for atomicity per plan E4b step 2 — a
	// co-holder run is either fully bound (own claims acquired AND
	// co-held claims registered) or not bound at all.
	//
	// `@blessed-invariant 13`: the holders set is the auto-terminal's input.
	if err := insertCoHolderClaimHoldersAtAcquire(ctx, args, tx, cand, nodeDef, tmpl); err != nil {
		return acquisition{}, false, fmt.Errorf("tryAcquire: co-holder rows: %w", err)
	}
	// @constraint: bind co-held addresses into the dispatch-time
	// `ExecuteRequest` — load the upstream claim handles' addresses per
	// alias and stash on the acquisition. Same lookup the substitution
	// context uses (loadInheritedClaimsForNode); read once and reuse.
	if held := loadInheritedClaimsForNode(ctx, args, tx, nd); len(held) > 0 {
		out.HeldClaims = held
	}

	loadResumeMetadataIfParked(ctx, args, tx, &out, cand)
	// @constraint: load the dispatch row's executor-attached scratch
	// (inline or spilled-handle materialized via Blob) so
	// buildExecuteRequest can populate ExecuteRequest.scratch on the
	// wire. Scratch round-trips across every prior-dispatch disposition;
	// the enqueue-side carry already stamps the row, this is the
	// read-side surfacing.
	//
	// @concept: executor
	loadScratchIntoAcquisition(ctx, args, tx, &out, cand)
	return out, true, nil
}

// restampLinkedSubClaimHolders re-stamps the holder_supervisor_id of
// every ACTIVE sub-claim row linked to this leaf run (node_run_id =
// this dispatch id AND parent_claim_handle_id set) onto the acquiring
// supervisor. Sub-claim rows are INSERTed under the PARENT acquirer's
// supervisor (`runner_subclaim.go::AcquireSubClaims`) and repointed to
// the leaf run by `child_execution.go::DispatchChildren`; the
// acquiring supervisor becomes the row's legitimate holder HERE, in
// the same tx as the leaf's dispatch-row claim, so the leaf-terminal
// claimant guards (Promote, the held-claim CheckAndFireResolution,
// heartbeat extension) all pass under ≥2-supervisor deployments.
//
// Guard discipline: the restamp is a CAS from the holder read in this
// same tx (`ReassignHolderSupervisor`'s
// `WHERE state='active' AND holder_supervisor_id = <observed>`), so a
// concurrent resolution or competing handoff surfaces as the
// errAcquireRestampLost sentinel and aborts the candidate's tx rather
// than silently stealing the row (@blessed-invariant 4). The sentinel
// scopes the abort to THIS candidate — the batch loop logs and moves
// on to the next candidate, since a lost CAS is an expected
// concurrent-supervisor race. A real persistence error still surfaces
// as an ordinary error and aborts the whole batch.
//
// Rows whose holder already equals args.SupervisorID (the
// single-supervisor common path, and the leaf's own freshly-Open'd
// claims) are skipped — zero extra writes.
//
// @concept: claim-tree
// @concept: fan-out
func restampLinkedSubClaimHolders(
	ctx context.Context, args RunArgs, tx persistence.Tx, cand persistence.Candidate,
) error {
	ch := args.ClaimHandles
	if ch == nil {
		return nil
	}
	rows, err := ch.ListByNodeRun(ctx, cand.DispatchID, tx)
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
				// @deliberate: CAS lost — a concurrent resolution or
				// competing handoff changed the holder between the read and
				// the guarded UPDATE. Expected race under ≥2 supervisors;
				// signal the skip-this-candidate sentinel so the batch loop
				// moves on instead of aborting every remaining candidate.
				return errAcquireRestampLost
			}
			return fmt.Errorf("restampLinkedSubClaimHolders: sub-claim %s (holder %s → %s): %w",
				row.ID, *row.HolderSupervisorID, args.SupervisorID, err)
		}
	}
	return nil
}

// bindLeafCandidateHandles resolves a fan-out leaf's per-partition
// candidate handle onto its matching AcquiredLock (E4).
//
// The candidate handle was minted at fan-out acquisition on the parent
// run (`AcquireSubClaims` → `DataProcessing.BeginCandidate`) and persisted
// on the sub-claim row; `DispatchChildren` then repointed that row's
// node_run_id to this leaf run. So the leaf finds its own candidate handle
// by querying claim_handle rows where `node_run_id = this leaf's dispatch
// id` and selecting the sub-claim (the one carrying both a non-empty
// `producer_candidate_handle` and a set `parent_claim_handle_id`).
//
// A leaf may own more than one claim_handle row keyed by this node_run_id:
// the fresh parent-selector claim it Open'd at acquisition (no candidate
// handle, no parent) plus the linked sub-claim. We attach the sub-claim's
// candidate handle to the leaf lock whose producer matches; on the typical
// single-fan-out-claim leaf there is exactly one such lock.
//
// Best-effort: a lookup error logs and leaves the candidate empty — the
// leaf still dispatches (degrading to the pre-E4 wire shape) rather than
// failing on a non-load-bearing read.
//
// @concept: fan-out
// @concept: data-processing
func bindLeafCandidateHandles(ctx context.Context, args RunArgs, tx persistence.Tx, out *acquisition, cand persistence.Candidate) {
	if out == nil || len(out.Locks) == 0 {
		return
	}
	ch := args.ClaimHandles
	if ch == nil {
		return
	}
	rows, err := ch.ListByNodeRun(ctx, cand.DispatchID, tx)
	if err != nil {
		args.Logger.Warn("bindLeafCandidateHandles: ListByNodeRun failed; leaf candidate_handle left empty",
			"run_id", cand.DispatchID.String(),
			"error", err.Error())
		return
	}
	for i := range rows {
		row := rows[i]
		// @constraint: only sub-claim rows (parent_claim_handle_id set)
		// carry a leaf candidate handle. Skip the leaf's own freshly-Open'd
		// parent-selector claim and any non-DataProcessing producer (empty
		// handle).
		if row.ParentClaimHandleID == nil || len(row.ProducerCandidateHandle) == 0 || row.ProducerName == nil {
			continue
		}
		for j := range out.Locks {
			sp, ok := out.Locks[j].Spec.(claimproducer.ClaimSpec)
			if !ok || sp.ProducerName != *row.ProducerName {
				continue
			}
			// @deliberate: don't overwrite an already-bound handle —
			// defensive against a future multi-fan-out-claim leaf; first
			// match wins per claimed_at ordering.
			if len(out.Locks[j].ProducerCandidateHandle) > 0 {
				continue
			}
			out.Locks[j].ProducerCandidateHandle = row.ProducerCandidateHandle
			break
		}
	}
}
