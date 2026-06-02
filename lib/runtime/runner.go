// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Omnibus runner — the stores redesign per-claim-cycle execution path.
//
// One call to RunNode picks an eligible candidate, runs the §7.3
// atomic acquisition transaction (candidate selection, in-Go
// eligibility, advisory locks, claim+lock-holder inserts, in-tx
// ClaimProducer.Open for ClaimSpec acquisitions), runs the
// verify-before-run guard (§7.3 step 4), transitions the node to
// running (§7.3 step 4.5), resolves attribute source-directives,
// determines the dispatch path, runs the heartbeat loop, and applies
// the terminal event.
//
// Helpers split across files for readability:
//   - runner_acquire.go  — §7.3 atomic acquisition + verify-before-run
//   - runner_dispatch.go — dispatch path (executor / native)
//   - runner_terminal.go — terminal-event handling
//
// @blessed-invariant 3: Multi-lock acquisition uses deterministic
// sorted order. (Spec §4.10 invariant 3.) For each candidate dispatch
// all per-spec lock acquisitions (named, scope) are performed in
// `(lock_kind, sort_key)` order to prevent deadlock under concurrent
// contention on overlapping lock sets. The sort happens in
// runner_locks.go's `sortLockSpecs`; the per-named-lock advisory
// locks, the scope re-evaluation, and the per-spec ClaimProducer.Open +
// lock-holder INSERT loop all walk the same sorted slice. Removing
// the sort or walking it in a different order in any of those steps
// reintroduces the deadlock the invariant guards against.
//
// @blessed-invariant 5: Verify-before-run. (Spec §4.10 invariant 5.)
// After the acquisition tx commits, the runner does a separate read
// of `rimsky_node_runs.claimed_by` and bails to the orphan-claim-lost-
// race handler if ownership has moved. The read happens in
// runner_acquire.go's `verifyBeforeRun` and is intentionally outside
// the acquisition tx — running the check inside the tx would race
// with other supervisors that also see the row as theirs because of
// MVCC snapshot isolation; the bail here is what catches the rare
// cross-transaction handoff and keeps the ownership invariant intact.
//
// @blessed-invariant 10: Lock acquisition is atomic with parent-run
// claim acquisition. Per spec §4.10 + the 2026-05-15 data-platform-
// extensions §Recursive scope partitioning: the §7.3 atomic acquisition
// transaction either claims the parent run AND inserts the parent
// `rimsky_claim_handles` row AND inserts all sub-claim handle rows for
// opted-into partitioning (via `ClaimProducer.SplitScope`) AND records
// the `ClaimProducer.Open`-returned addresses AND registers any co-holder
// `rimsky_claim_holders` rows declared by the node's template (`holds:`),
// or none of these. The producer's own state
// mutations run in a producer-internal transaction decoupled from
// rimsky's — the v2 tx-sharing mechanism (`locks.WithTx`) is gone.
// Single-writer-per-scope (invariant 4b) holds because rimsky's conflict
// predicate gates claim-handle INSERTs against `rimsky_claim_handles`
// only — producer orphan state is invisible to the predicate.
//
// Post-stage-5 of the run-row lifecycle cutover: claim-holders rows
// land in the same acquisition tx, keyed by `holder_run_id` (this
// run's `rimsky_node_runs.id`). A run is either fully bound (own
// claims acquired AND co-held claims registered) or not bound at all.
package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	attributes "github.com/rimsky-ai/rimsky-core/lib/graph/attribute"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

// RunnerResult is the outcome of a single RunNode invocation.
type RunnerResult struct {
	// Ran is true iff the runner committed the acquisition tx and started
	// (or at least attempted) the dispatch path. False when no candidate
	// was eligible, or when the verify-before-run guard fired.
	Ran bool
	// Async is true when the executor emitted AwaitAsyncCallback; the
	// runner returns immediately and the callback registry takes over.
	Async bool
	// AsyncAckID is populated when Async is true.
	AsyncAckID string
	// NodeID, DispatchID identify the candidate the runner committed to.
	// Zero on Ran=false.
	NodeID     shared.UUID
	DispatchID shared.UUID
}

// RunArgs is the caller-supplied dependency bundle for RunNode.
//
// The runner needs a richer surface than the pre-redesign per-dispatch
// runner: it picks its own candidate (no DispatchID/NodeID input), needs
// the persistence.Tables to drive the §7.3 transaction directly, needs the
// store registry for ClaimProducer.Open / Commit / Abandon / Release
// dispatch, and needs the claim-handles accessor for INSERT / DELETE /
// UpdateAddress.
type RunArgs struct {
	// Persist is the unified persistence.Tables handle (rimsky_* tables).
	// Required. The runner opens the §7.3 acquisition tx via
	// Persist.Transaction so queue helpers, lock-holder INSERTs, and
	// claim-holder INSERTs all participate in the same tx.
	Persist persistence.Tables
	// Queue exposes the queue-side helpers (SelectCandidates,
	// ClaimDispatchRow, RefreshHeartbeat, …) that participate in the
	// §7.3 acquisition tx.
	Queue persistence.Queue
	// AdvisoryLocker carries the cross-process synchronization primitives
	// (per-named-lock advisory locks, per-scope advisory locks, etc.)
	// the acquisition tx threads through.
	AdvisoryLocker persistence.AdvisoryLocker
	// ClaimHandles is the rimsky_claim_handles accessor. Reachable via
	// Persist.ClaimHandles(); kept as an explicit field so call sites
	// don't have to thread Persist + ClaimHandles separately.
	ClaimHandles persistence.ClaimHandleTable
	// StoreRegistry is the per-process store registry built at supervisor
	// startup from rimsky.yml (spec §6.1). The runner dispatches against
	// the 4-verb locks.ClaimProducer interface (Open/Commit/Abandon/Release).
	StoreRegistry *locks.Registry
	// NamedLocks is the operator-side named-lock config (limits per
	// name). The §7.3 acquisition path enforces counter-semaphore
	// semantics: under the per-name advisory lock, the unexpired
	// lock-holder count must be < limit before insert. Empty / missing
	// → no limits enforced (limit defaults to ∞).
	NamedLocks locks.NamedLocksConfig

	Clock        shared.Clock
	Logger       shared.Logger
	SupervisorID string
	// AcceptedExecutors / AcceptedStores are the supervisor pool's
	// accept-lists. Threaded into SelectCandidates' dispatch SELECT so
	// the runner only considers rows the pool can satisfy (spec §6.2).
	AcceptedExecutors []string
	AcceptedStores    []string

	Pool        *executor.ClientPool
	Resolver    executor.Resolver
	CallbackURL string

	// HeartbeatInterval drives the §7.3 step 3 lock-holder ExpiresAt
	// budget (5 × heartbeatInterval) and the in-loop heartbeat tick.
	// Zero falls back to 5s.
	HeartbeatInterval time.Duration
	// ResumeGrace is the preserve-for-resume cutoff. Zero falls back
	// to 30 minutes.
	ResumeGrace time.Duration
	// SelectCandidatesLimit caps the dispatch SELECT batch size in the
	// §7.3 step 1 candidate read. Zero falls back to 8 — small enough
	// that one supervisor doesn't monopolise a candidate page, large
	// enough that a single ineligible candidate doesn't stall the tick.
	SelectCandidatesLimit int

	// Blob is the active BlobBackend (loaded from BlobConfig at startup).
	// Used by applyTerminalPark to spill large parked-payload bytes and
	// by the H1 named-event persist path to spill large event payloads.
	// Nil means "spill disabled" — callers store inline regardless of
	// size (legacy behavior). Required when BlobSpillThreshold > 0.
	Blob persistence.BlobBackend
	// BlobSpillThreshold is the spill cutoff in bytes; payloads larger
	// than this are written through Blob instead of stored inline.
	// Zero means "spill disabled" (everything inline). Default applied
	// at startup is BlobConfig.SpillThresholdBytes (default 64KB).
	BlobSpillThreshold int

	// MaxRetriesWithoutProgressDefault is the deployment-level cap on
	// consecutive retries with no settling_signal_type change before the
	// runner forces an Error{error_class: "retry_loop_no_progress"}
	// terminal.
	// Zero means "use the built-in default of 100"; per-row override on
	// rimsky_node_runs.max_retries_without_progress takes
	// precedence (NULL on the row falls back to this default; 0 on the
	// row disables the cap entirely).
	MaxRetriesWithoutProgressDefault int

	// InvalidateHandler is the unified entry point for invalidate
	// requests originating from the supervisor's own handlers (E3
	// SweepParkedNodes wake, H2 on_event-handler-emitted invalidates).
	// Optional; when nil the runtime falls back to InvalidateNode
	// directly.
	InvalidateHandler func(ctx context.Context, args InvalidateArgs) error

	// ExpectedAttributesSchemaFor returns the executor's advertised
	// expected_attributes_schema bytes (JSON Schema) plus an ok flag
	// (false for unknown executors). Used by
	// `computeEffectiveAttributeSchema` (runner_dispatch.go) to merge
	// the executor's schema into the per-node effective attribute
	// schema at dispatch.
	//
	// Wired in cmd/rimsky-supervisor/main.go from the discovery cache
	// (the same `disc` value that previously fed UserdataValidator
	// before the 2026-05-21 userdata collapse).
	//
	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)

	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook (plan I1/I2/I3). Optional. The interface is intentionally
	// minimal — counter/observer style — so foundation has no
	// prometheus dependency. Production wiring constructs an adapter
	// over control/observability.MetricsRegistry; tests pass nil.
	Metrics MetricsHook

	// DataProcessors resolves a producer name to a
	// `DataProcessingClient` for the fan-out / candidate / version
	// surface (`BeginCandidate` / `CommitCandidate` /
	// `AbandonCandidate`). Threaded through so the supervisor's
	// sub-claim acquisition path (`AcquireSubClaims`) and the
	// auto-terminal Commit path can dispatch on producers that
	// advertise the `data_processing` protocol in their
	// `protocols:` block. Nil → the candidate-handle slot stays empty
	// and the leaf executor falls back to claim_scope_data + parent address
	// alone (still correct for non-DataProcessing producers). Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Protocol surfaces / DataProcessing.
	DataProcessors DataProcessingRegistry

	// LifecycleSubs is the supervisor's outbound LifecycleSubscriber
	// registry, threaded from runtime.Config. Used by the sub-graph and
	// fanout-partition RunScope close sites to fire OnRunScopeTerminal via
	// FanOutRunScopeEvent. Nil → run-scope fan-out is a no-op.
	//
	// Per spec 2026-05-24-host-agent-and-proxy-design.md.
	LifecycleSubs *locks.LifecycleRegistry
	// LifecyclePeersForSpec resolves the late-bind-aware peer set for a
	// template at run-scope close. Function pointer populated at the cmd/
	// entrypoint so runtime/ never imports control/. Nil → fan-out no-op.
	LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string
	// LateBindServiceProxies maps protocol name → proxy service name
	// (rimsky.yml late_bind_service_proxies). Consulted at the §7.3
	// SelectCandidates call so the dispatch SELECT admits the proxy peer
	// as a stand-in for late-bound executor / claim-producer references.
	// Empty → the admit-list extension stays inert.
	LateBindServiceProxies map[string]string
}

// MetricsHook is the metric-instrumentation surface foundation calls
// at the integration points (dispatch start/end, terminal verdict,
// invalidate, claim acquisition, parked sweep, etc.). Each method is a
// no-op when the hook is nil-shaped (the runner threads a non-nil
// no-op default rather than nil-checking each call site).
//
// Gauge-setters (`SetNodesByState`, `SetParkedByReason`,
// `SetHeldFrames`, `SetNodeRunsPending`) are intentionally NOT on
// this interface — foundation does not refresh gauges; the control-
// layer's `*RegistryHook.StartGaugeRefresher` polls persistence and
// calls its own concrete setters. Splitting keeps the foundation-
// visible surface tight to the call sites that actually exist here.
type MetricsHook interface {
	// IncDispatch records a dispatch start (executor + terminal class
	// "started"); pair with IncTerminal at the resolved terminal.
	IncDispatch(executor, terminalClass string)
	// IncTerminal records a resolved terminal verdict.
	IncTerminal(terminalClass, errorClass string)
	// IncInvalidate records an invalidate fired by source ("admin" |
	// "scheduler" | "handler" | "policy").
	IncInvalidate(sourceKind string)
	// IncClaimAcquisition records a claim acquisition (producer name +
	// intent: "acquired" | "unavailable" | "abandon").
	IncClaimAcquisition(producer, intent string)
	// IncNamedEvent records a NamedEvent persistence write.
	IncNamedEvent(executor, eventName string)
	// ObserveDispatchLatency observes the wall-clock dispatch duration.
	ObserveDispatchLatency(executor string, seconds float64)
	// ObserveClaimAcquisitionLatency observes the wall-clock claim
	// acquisition tx duration.
	ObserveClaimAcquisitionLatency(producer string, seconds float64)
	// ObserveFrameDuration observes a frame's wall-clock duration.
	ObserveFrameDuration(seconds float64)
	// ObserveParkedDurationOnResume observes how long a node spent
	// parked, sampled at resume time.
	ObserveParkedDurationOnResume(seconds float64)
}

// noopMetrics is the silent default used when args.Metrics is nil.
// Returning this from a helper rather than nil-checking each call site
// keeps the call sites concise.
type noopMetrics struct{}

func (noopMetrics) IncDispatch(string, string)                     {}
func (noopMetrics) IncTerminal(string, string)                     {}
func (noopMetrics) IncInvalidate(string)                           {}
func (noopMetrics) IncClaimAcquisition(string, string)             {}
func (noopMetrics) IncNamedEvent(string, string)                   {}
func (noopMetrics) ObserveDispatchLatency(string, float64)         {}
func (noopMetrics) ObserveClaimAcquisitionLatency(string, float64) {}
func (noopMetrics) ObserveFrameDuration(float64)                   {}
func (noopMetrics) ObserveParkedDurationOnResume(float64)          {}

// metricsOf returns args.Metrics or noopMetrics.
func metricsOf(args RunArgs) MetricsHook {
	if args.Metrics == nil {
		return noopMetrics{}
	}
	return args.Metrics
}

// AsyncContext is the per-async-handoff context the runner hands to the
// supervisor's callback registry (see callback.go). The registry resolves
// an incoming callback's ackID back to this AsyncContext, which carries
// everything the terminal-event handler needs to finalize the node.
//
// The shape mirrors `acquisition` plus the per-dispatch attribute
// state — the callback path reconstructs both before invoking
// `applyTerminal*` so the same per-lock release tx, resolution
// dispatch, state-transition, and event-emission flow runs whether
// the terminal arrived synchronously over the executor RPC or
// asynchronously via HTTP callback.
type AsyncContext struct {
	NodeID        shared.UUID
	InstanceID    shared.UUID
	DispatchID    shared.UUID
	SupervisorID  string
	StoreRegistry *locks.Registry
	// FrameID is the dispatch row's frame_id — propagated through async
	// handoff so the terminal handler can re-enqueue retries with the
	// correct frame_id (per blessed-invariant 19).
	FrameID shared.UUID
	// AcquiredLocks is the set of lock-holder rows the runner inserted
	// during acquisition. The terminal handler walks these in sort
	// order (deterministic per blessed-invariant 3) to drive Commit +
	// Release + per-claim resolution dispatch.
	AcquiredLocks []AcquiredLock
	// NodeType is the candidate's template node type. Used by the
	// terminal handler when classifying actions and when re-enqueueing
	// on retry / infra_reenqueue.
	NodeType string
	// Executor mirrors `acquisition.Executor` — the runner needs it to
	// re-enqueue the dispatch row on retry / infra_reenqueue branches.
	Executor string
	// NodeDef is the candidate's per-node-type template definition. The
	// terminal handler reads it for the policy chain (`error_types`).
	// May be nil when the runner could not locate a matching def at
	// acquisition.
	NodeDef *node.TemplateNodeDef
	// ResolvedAttributes is the post-substitution attribute map the
	// runner produced at dispatch time. The Success-outcome branch of
	// the terminal handler merges the executor's `attributes_delta`
	// into this map and validates the result.
	ResolvedAttributes map[string]any
	// AttributesSchema is the per-node-type JSON schema fragment the
	// terminal handler validates against on a Success outcome with
	// non-empty delta. Source: `NodeDef.Attributes.Schema`.
	AttributesSchema map[string]any
}

// AcquiredLock bundles a lock-holder row with its originating spec and
// (for ClaimSpec) the producer-returned ClaimResult. The runner builds
// one of these per successful Open call; the terminal handler walks
// the slice in deterministic sort order (blessed-invariant 3) to drive
// Commit/Abandon and the auto-terminal subgraph check.
type AcquiredLock struct {
	// Spec is one of locks.NamedLockSpec or claimproducer.ClaimSpec.
	Spec any
	// ClaimHandleID is the rimsky_claim_handles row id created at
	// acquisition. Drives all subsequent claimant-guarded mutations.
	ClaimHandleID shared.UUID
	// ClaimResult is populated by Open for ClaimSpec acquisitions;
	// zero for NamedLockSpec.
	ClaimResult claimproducer.ClaimResult
	// Producer is the resolved ClaimProducer. Nil for NamedLockSpec;
	// non-nil for ClaimSpec.
	Producer locks.ClaimProducer
	// Alias is the per-claim alias for ClaimSpec acquisitions; "" for
	// NamedLockSpec.
	Alias string
	// IsHeld carries the `is_held` value written to the claim_handle
	// row. Sub-claim acquisition (`AcquireSubClaims`) propagates this
	// into per-sub-claim INSERTs so the rows persist past the leaf
	// active-terminal until the parent's recursive resolution walks
	// them. Always false for NamedLockSpec.
	IsHeld bool
	// ProducerCandidateHandle carries the per-sub-claim candidate handle
	// (returned by `DataProcessing.BeginCandidate`) for a fan-out LEAF's
	// own sub-claim. Empty for the parent fan-out run, for non-fan-out
	// runs, and for named locks. Populated at leaf acquisition by
	// resolving the sub-claim row whose `node_run_id` equals this leaf's
	// dispatch id (linked in `fanout_dispatch.go::CreateFanOutChildren`),
	// then carried onto the wire by `makeClaimHandle` as
	// `StoreHandle.candidate_handle` (E4). Inert in rimsky per
	// @blessed-invariant 20.
	ProducerCandidateHandle []byte
}

// RunNode runs one full claim-and-execute cycle. The eight-stage outline
// of the omnibus runner is laid out here as a tall function with named
// helpers; each stage either succeeds and continues or bails to a
// recoverable handler.
//
// Returns:
//   - {Ran:false}, nil          — no candidate eligible / verify-before-run
//     lost / state machine rejected fresh→running
//   - {Ran:true, Async:true}    — dispatched; terminal arrives via callback
//   - {Ran:true}                — terminal handled inline (sync path)
//
// On a non-recoverable internal error the function returns {Ran:false}
// and the error; callers log and move on. Recoverable conditions
// (executor stream failure, terminal classification) are handled inline
// via the terminal handler.
//
// @concept: supervisor (this claim-and-execute cycle is the supervisor's
// core behavior: acquisition tx, dispatch, terminal handling, auto-terminal)
func RunNode(
	ctx context.Context,
	args RunArgs,
	registerAsync func(ackID string, actx AsyncContext),
) (RunnerResult, error) {
	if err := validateRunArgs(args); err != nil {
		return RunnerResult{}, err
	}
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	heartbeatInterval := args.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = 5 * time.Second
	}

	// Step 1 — §7.3 atomic acquisition + verify-before-run + state
	// transition.
	acq, ok, err := acquireCandidate(ctx, args, heartbeatInterval)
	if err != nil {
		return RunnerResult{}, err
	}
	if !ok {
		// No eligible candidate, or the candidate was lost via verify-
		// before-run / illegal transition; the helper has already
		// released store-side state (best-effort) and emitted
		// orphaned_claim_lost_race when warranted.
		return RunnerResult{Ran: false}, nil
	}

	// Step 2 (formerly OpenHandle) — retired: the store's Open
	// returns the address inside the acquisition tx; there is no
	// separate native-handle stage.

	// Step 2a — E7 fan-out dispatcher. When the acquisition returned
	// sub-claims (the node declared `fan_out:` and acquireCandidate
	// called SplitScope inside the acquisition tx), create one child
	// run per sub-claim and DEFER leaf-dispatch to the children. The
	// parent run stays `running`; the per-child state propagation
	// (`state_propagation.go::PropagateFromChildState`) settles the parent
	// at child-aggregate-terminal time.
	//
	// Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Fan-out template DSL "Mechanics at dispatch" steps 3-5.
	//
	//	@concept: fan-out
	//	@concept: run-scope
	if len(acq.SubClaims) > 0 && IsFanOutNode(acq.NodeDef) {
		if err := dispatchFanOutChildren(ctx, args, &acq); err != nil {
			return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, err
		}
		// Children dispatch independently on subsequent runner ticks
		// (their rows are now eligible candidates the SelectCandidates
		// helper will pick up). The parent run stays `running` until
		// the aggregator settles it.
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, nil
	}

	// Step 3 — resolve attribute source-directives. Failure here routes
	// through `applyAttributeFailure`, which inspects the typed error
	// returned by `resolveAttributes` and selects one of three policy
	// chains: `template_resolution_failed` (strict-directive miss),
	// `executor_schema_unavailable` (executor's expected schema not
	// visible at dispatch), or `template_validation_failed` (composition
	// violations, type mismatches, override-vs-schema conflicts).
	resolvedAttrs, attrSchema, err := resolveAttributes(ctx, args, &acq)
	if err != nil {
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID},
			applyAttributeFailure(ctx, args, &acq, err)
	}

	// Persist the substituted attributes ahead of dispatch so the
	// callback path (§12.5 incremental writeback) has a row to merge
	// into. Under per-run keying the row is keyed on the dispatch_id
	// (acq.DispatchID); each fresh dispatch starts a new row.
	if err := upsertAttributesPreDispatch(ctx, args, acq.DispatchID, acq.NodeID, resolvedAttrs); err != nil {
		log.Warn("runner: upsert attributes pre-dispatch failed",
			"run_id", acq.DispatchID.String(),
			"node_id", acq.NodeID.String(), "error", err.Error())
	}
	dispatchAttrs := resolvedAttrs

	// Step 4 — dispatch path.
	dctx := dispatchContext{
		Args:              args,
		Acquired:          &acq,
		Attributes:        dispatchAttrs,
		AttributesSchema:  attrSchema,
		HeartbeatInterval: heartbeatInterval,
		Log:               log,
		RegisterAsync:     registerAsync,
	}
	terminal, asyncResult, err := dispatch(ctx, dctx)
	if err != nil {
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, err
	}
	if asyncResult != nil {
		return *asyncResult, nil
	}

	// Step 6 — terminal event handling. Pass the dispatch-time attribute
	// view so the commit path's mergeAttributesDelta starts from the
	// same map the executor saw (resumed runs include preserved
	// executor-populated fields). runApplyTerminal wraps the call in
	// the outer state-mutation tx that applyTerminal threads through
	// every handler — same shape the async-callback path uses, so the
	// determinism invariant holds at both sites.
	if err := runApplyTerminal(ctx, args, &acq, dispatchAttrs, attrSchema, terminal, nil); err != nil {
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, err
	}

	// Breakpoint checkpoint: after_terminal. Runs AFTER runApplyTerminal
	// returns (its tx is committed) and BEFORE the next runner tick can
	// fire any downstream cascade work. EvaluateBreakpoints opens its own
	// short txns; pause-mode hits block on waitForResume (per-iteration
	// short txns; no tx held across the wait). The return value is
	// discarded — after-terminal overlays don't mutate further dispatch
	// because the dispatch is already complete. Pause-mode breakpoints at
	// after_terminal block the runner before it returns control to the
	// supervisor loop; that's the value. Notify-only breakpoints just
	// observe. Failures are best-effort: Warn-log and continue so
	// debugger problems don't fail the run.
	scope := resolveAcqScope(ctx, args, &acq)
	terminalSig := signalForTerminal(terminal)
	if _, err := EvaluateBreakpoints(ctx, args, CheckpointContext{
		InstanceID:       acq.InstanceID,
		DispatchID:       acq.DispatchID,
		FrameID:          acq.FrameID,
		Executor:         acq.Executor,
		NodeType:         acq.NodeType,
		Graph:            acq.GraphName,
		ChildKey:         scope.PartitionKey,
		MergedAttributes: acq.MergedAttributes,
		Checkpoint:       persistence.CheckpointAfterTerminal,
		TerminalSignal:   &terminalSig,
		NodeRunSnapshot:  nodeRunSnapshotForBreakpoint(&acq),
		HeldClaims:       heldClaimsSummaryForBreakpoint(&acq),
		OpenWaitSet:      openWaitSetSummaryForBreakpoint(ctx, args, &acq),
	}); err != nil && log != nil {
		log.Warn("breakpoint: after_terminal eval failed; continuing",
			"dispatch_id", acq.DispatchID.String(),
			"error", err.Error())
	}
	return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, nil
}

// validateRunArgs checks the construction-time invariants. Returns an
// error rather than panicking so the caller (supervisor.runLoop) can
// shut down cleanly.
func validateRunArgs(args RunArgs) error {
	if args.Persist == nil {
		return errors.New("supervisor.RunNode: Persist is required")
	}
	if args.Queue == nil {
		return errors.New("supervisor.RunNode: Queue is required")
	}
	if args.AdvisoryLocker == nil {
		return errors.New("supervisor.RunNode: AdvisoryLocker is required")
	}
	if args.ClaimHandles == nil {
		return errors.New("supervisor.RunNode: ClaimHandles is required")
	}
	if args.StoreRegistry == nil {
		return errors.New("supervisor.RunNode: StoreRegistry is required")
	}
	if args.SupervisorID == "" {
		return errors.New("supervisor.RunNode: SupervisorID is required")
	}
	if args.Resolver == nil {
		return errors.New("supervisor.RunNode: Resolver is required")
	}
	if args.Pool == nil {
		return errors.New("supervisor.RunNode: Pool (executor.ClientPool) is required")
	}
	return nil
}

// upsertAttributesPreDispatch writes the substituted attributes object
// to rimsky_node_attributes so the callback handler has a row to merge
// into. Under per-run keying the row is keyed on runID; each dispatch
// is a fresh row by PK, no prior-row read needed.
//
// Resume detection lives in the store (the store detects
// resumed-vs-fresh by lookup against its own state keyed by lock-
// holder identity). The supervisor no longer threads a
// resumed-from-rebind flag, so the row is replaced outright; the
// executor's incremental MergeDelta calls are the canonical channel
// for executor-populated fields.
func upsertAttributesPreDispatch(
	ctx context.Context,
	args RunArgs,
	runID, nodeID shared.UUID,
	resolvedAttrs map[string]any,
) error {
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.NodeAttributes().Upsert(ctx, runID, nodeID, resolvedAttrs, tx)
	})
}

// emitAttributeFailureEvent appends the typed event for a dispatch-time
// attribute failure. `kind` is the event name (`template_resolution_failed`,
// `template_validation_failed`, or `executor_schema_unavailable`) and
// determines how operator tooling routes the surface; the payload shape
// is identical across all three classes.
func emitAttributeFailureEvent(
	ctx context.Context, args RunArgs, nodeID, instanceID shared.UUID, kind, directive, site, field, reason string,
) {
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &nodeID, InstanceID: &instanceID,
			Kind: kind,
			Payload: map[string]any{
				"directive": directive,
				"site":      site,
				"field":     field,
				"reason":    reason,
			},
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("emitAttributeFailureEvent: append event failed",
			"node_id", nodeID.String(),
			"instance_id", instanceID.String(),
			"kind", kind,
			"directive", directive,
			"error", err.Error())
	}
}

// applyAttributeFailure routes an attribute resolution / validation
// failure through the correct policy chain based on the error type.
// Three distinct classes are recognised:
//
//   - `*attributes.ErrMissingSource` → `template_resolution_failed`
//     (strict-directive miss; the canonical retry-after-cascade case)
//   - `*executorSchemaUnavailableError` → `executor_schema_unavailable`
//     (executor's expected_attributes_schema not visible at dispatch;
//     distinct so operators can retry after handshake completes)
//   - `*attributeValidationError` → `template_validation_failed`
//     (composition violations, dispatch-bag JSON Schema failures,
//     executor-schema mismatches at dispatch)
//
// Any unrecognised error type falls back to `template_resolution_failed`
// (defensive). Per spec
// .ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md
// §"Error handling".
//
// State of the node is moved `running → stale` (or failed) per the
// resolved action; lock-holder rows are released via the give-up
// branch of applyErrorPolicy.
func applyAttributeFailure(
	ctx context.Context, args RunArgs, acq *acquisition, err error,
) error {
	class, eventKind := classifyAttributeFailure(err)
	emitAttributeFailureEvent(ctx, args, acq.NodeID, acq.InstanceID,
		eventKind, extractDirective(err), "attribute", "", err.Error())
	// applyErrorPolicy now expects to run inside an outer state-mutation
	// tx (per @blessed-invariant: Callback determinism). Wrap it in a
	// fresh tx here — this caller is the dispatch-time attribute
	// resolution path, which has no outer tx of its own, so we open one
	// and run the returned postCommit after commit. Same shape as
	// runApplyTerminal but for the error-only (no terminal verdict)
	// path.
	var postCommit postCommitFn
	if txErr := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, perr := applyErrorPolicy(ctx, args, acq, class,
			map[string]any{"error": err.Error()}, tx)
		if perr != nil {
			return perr
		}
		postCommit = pc
		return nil
	}); txErr != nil {
		return txErr
	}
	if postCommit != nil {
		postCommit(ctx)
	}
	return nil
}

// classifyAttributeFailure inspects the error chain and returns the
// (error_class, event_kind) pair used to route the failure. The two
// names are kept equal for each class to simplify downstream tooling;
// the split exists in case the event surface ever needs to evolve
// independently of the policy class.
func classifyAttributeFailure(err error) (string, string) {
	var miss *attributes.ErrMissingSource
	if errors.As(err, &miss) {
		return "template_resolution_failed", "template_resolution_failed"
	}
	var schemaUnavail *executorSchemaUnavailableError
	if errors.As(err, &schemaUnavail) {
		return "executor_schema_unavailable", "executor_schema_unavailable"
	}
	var validation *attributeValidationError
	if errors.As(err, &validation) {
		return "template_validation_failed", "template_validation_failed"
	}
	// Defensive fallback: anything we didn't classify routes through the
	// resolution chain. Preserves backwards-compatible behaviour for
	// errors that didn't go through resolveAttributes' typed wrappers.
	return "template_resolution_failed", "template_resolution_failed"
}

// extractDirective digs the directive name out of an *attributes.ErrMissingSource
// when present; returns empty otherwise.
func extractDirective(err error) string {
	var miss *attributes.ErrMissingSource
	if errors.As(err, &miss) {
		return miss.Directive
	}
	return ""
}

// applyErrorPolicy funnels an application-level terminal through
// the OnError policy chain. Defined in runner_terminal.go.
