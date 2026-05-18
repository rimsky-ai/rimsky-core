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
// / inheritor `rimsky_claim_holders` rows declared by the node's template
// (`holds:` / `inherits:`), or none of these. The producer's own state
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

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	attributes "github.com/fallguy/rimsky/graph/attribute"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/runtime/executor"
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
	// consecutive retries with no last_outcome change before the runner
	// forces an Error{error_class: "retry_loop_no_progress"} terminal.
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

	// UserdataValidator runs the executor's advertised JSON Schema (if
	// any) against the post-merge userdata bytes at dispatch time. Plan
	// F7. Userdata is inert in Rimsky per @blessed-invariant 11 — no
	// substitution pass is run on userdata, so the bytes the validator
	// sees are exactly the result of the deep-merge across template,
	// by_executor, and by_node fragments (graph/shared.DeepMergeJSON).
	// Do NOT add a substitution pre-pass here; the invariant forbids it.
	//
	// Returns nil to indicate validation passed (or no schema is known
	// for the executor); a non-nil error routes the dispatch through
	// the on_executor_errored handler with
	// error_class="userdata_validation_failed".
	//
	// The hook lives outside runtime so the jsonschema
	// dependency stays in the graph layer (foundation has a strict
	// dependency budget — stdlib + pgx + uuid + modernc/sqlite).
	UserdataValidator func(executorName string, merged map[string]any) error

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
	// and the leaf executor falls back to scope_data + parent address
	// alone (still correct for non-DataProcessing producers). Spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Protocol surfaces / DataProcessing.
	DataProcessors DataProcessingRegistry
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
	// Spec is one of locks.NamedLockSpec or locks.ClaimSpec.
	Spec any
	// ClaimHandleID is the rimsky_claim_handles row id created at
	// acquisition. Drives all subsequent claimant-guarded mutations.
	ClaimHandleID shared.UUID
	// ClaimResult is populated by Open for ClaimSpec acquisitions;
	// zero for NamedLockSpec.
	ClaimResult locks.ClaimResult
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
	//	@concept: run-tree
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

	// Step 3 — resolve attribute source-directives. Failure here raises
	// template_resolution_failed and routes through the policy chain.
	resolvedAttrs, attrSchema, err := resolveAttributes(ctx, args, &acq)
	if err != nil {
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID},
			applyTemplateResolutionFailure(ctx, args, &acq, err)
	}

	// Persist the substituted attributes ahead of dispatch so the
	// callback path (§12.5 incremental writeback) has a row to merge
	// into. RunAttempt is bumped from any prior row's attempt.
	if err := upsertAttributesPreDispatch(ctx, args, acq.NodeID, resolvedAttrs); err != nil {
		log.Warn("runner: upsert attributes pre-dispatch failed",
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
	// executor-populated fields).
	if err := applyTerminal(ctx, args, &acq, dispatchAttrs, attrSchema, terminal); err != nil {
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, err
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
// into. Bumps `run_attempt` from any prior row's value.
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
	nodeID shared.UUID,
	resolvedAttrs map[string]any,
) error {
	return args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		prior, _ := args.Persist.NodeAttributes().Get(ctx, nodeID, tx)
		attempt := 1
		if prior != nil {
			attempt = prior.RunAttempt + 1
		}
		return args.Persist.NodeAttributes().Upsert(ctx, nodeID, attempt, resolvedAttrs, tx)
	})
}

// emitTemplateResolutionFailedEvent appends the typed event for a
// dispatch-time substitution miss. Used by both the attribute-resolve
// path and the lock/scope pre-substitution path.
func emitTemplateResolutionFailedEvent(
	ctx context.Context, args RunArgs, nodeID, instanceID shared.UUID, directive, site, field, reason string,
) {
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return args.Persist.Events().Append(ctx, persistence.EventAppendInput{
			NodeID: &nodeID, InstanceID: &instanceID,
			Kind: "template_resolution_failed",
			Payload: map[string]any{
				"directive": directive,
				"site":      site,
				"field":     field,
				"reason":    reason,
			},
		}, tx)
	}); err != nil && args.Logger != nil {
		args.Logger.Warn("emitTemplateResolutionFailedEvent: append event failed",
			"node_id", nodeID.String(),
			"instance_id", instanceID.String(),
			"directive", directive,
			"error", err.Error())
	}
}

// applyTemplateResolutionFailure routes a substitution miss through the
// template_resolution_failed policy chain. State of the node is moved
// `running → stale` (or failed) per the resolved action; lock-holder
// rows are released via the give-up branch.
func applyTemplateResolutionFailure(
	ctx context.Context, args RunArgs, acq *acquisition, err error,
) error {
	emitTemplateResolutionFailedEvent(ctx, args, acq.NodeID, acq.InstanceID,
		extractDirective(err), "attribute", "", err.Error())
	return applyErrorPolicy(ctx, args, acq, "template_resolution_failed",
		map[string]any{"error": err.Error()})
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
