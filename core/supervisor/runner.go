// Omnibus runner — the stores redesign per-claim-cycle execution path.
//
// One call to RunNode picks an eligible candidate, runs the §7.3
// atomic acquisition transaction (candidate selection, in-Go
// eligibility, advisory locks, claim+lock-holder inserts, in-tx
// Store.Open for ClaimSpec acquisitions), runs the verify-before-run
// guard (§7.3 step 4), transitions the node to running (§7.3 step
// 4.5), resolves attribute source-directives, determines the dispatch
// path, runs the heartbeat loop, and applies the terminal event.
//
// Helpers split across files for readability:
//   - runner_acquire.go  — §7.3 atomic acquisition + verify-before-run
//   - runner_dispatch.go — dispatch path (executor / native)
//   - runner_terminal.go — terminal-event handling
//
// @blessed-invariant 3: Multi-lock acquisition uses deterministic
// sorted order. (Spec §4.10 invariant 3.) For each candidate dispatch
// all per-spec lock acquisitions (named, region) are performed in
// `(lock_kind, sort_key)` order to prevent deadlock under concurrent
// contention on overlapping lock sets. The sort happens in
// runner_locks.go's `sortLockSpecs`; the per-named-lock advisory
// locks, the region re-evaluation, and the per-spec Store.Open +
// lock-holder INSERT loop all walk the same sorted slice. Removing
// the sort or walking it in a different order in any of those steps
// reintroduces the deadlock the invariant guards against.
//
// @blessed-invariant 5: Verify-before-run. (Spec §4.10 invariant 5.)
// After the acquisition tx commits, the runner does a separate read
// of `rimsky_dispatch.claimed_by` and bails to the orphan-claim-lost-
// race handler if ownership has moved. The read happens in
// runner_acquire.go's `verifyBeforeRun` and is intentionally outside
// the acquisition tx — running the check inside the tx would race
// with other supervisors that also see the row as theirs because of
// MVCC snapshot isolation; the bail here is what catches the rare
// cross-transaction handoff and keeps the ownership invariant intact.
//
// @blessed-invariant 10: Lock acquisition is atomic with dispatch
// claim (rimsky-side). Per spec §4.10 (revised in v3): the §7.3
// atomic acquisition transaction either claims the dispatch row AND
// inserts every required `rimsky_lock_holders` row AND records the
// `Store.Open`-returned address, or none of these. The store's own
// state mutations run in a substrate-internal transaction decoupled
// from rimsky's — the v2 tx-sharing mechanism (`store.WithTx`) is
// gone. Single-writer-per-region (invariant 4b) holds because
// rimsky's conflict predicate gates lock-holder INSERTs against
// `rimsky_lock_holders` only — store orphan state is invisible to
// the predicate.
package supervisor

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/attributes"
	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// RunnerResult is the outcome of a single RunNode invocation.
type RunnerResult struct {
	// Ran is true iff the runner committed the acquisition tx and started
	// (or at least attempted) the dispatch path. False when no candidate
	// was eligible, or when the verify-before-run guard fired.
	Ran bool
	// Async is true when the executor emitted AsyncAccepted; the runner
	// returns immediately and the callback registry takes over.
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
// pool access to drive the §7.3 transaction directly, needs the store
// registry for Store.Open / Commit / Abandon / Release
// dispatch, and needs the lock-holders client for INSERT / DELETE /
// UpdateAddress.
type RunArgs struct {
	// Storage is the rimsky_* table accessor surface.
	Storage storage.StorageBackend
	// Queue exposes the queue-side helpers (SelectCandidates,
	// ClaimDispatchRow, RefreshHeartbeat, …) that participate in the
	// §7.3 acquisition tx.
	Queue queue.DispatchQueue
	// QueuePool is the *pgxpool.Pool the queue is bound to. The runner
	// opens the v3 §7.3 rimsky-side acquisition tx on this pool so the
	// queue helpers and the rimsky-side lock-holder + claim-holder
	// inserts all participate in the same tx. The substrate's `Open`
	// RPC fires inside this tx scope but the substrate runs its own
	// decoupled tx for substrate-side state mutation (v3 spec §7.3 step
	// 4); the two are not joined. core/queue/postgres.Queue exposes
	// Pool() to make this trivial; non-postgres queue impls would need
	// to wire their own pool.
	QueuePool *pgxpool.Pool
	// LockHolders is the database-facing helper for rimsky_lock_holders.
	// Source: storage.LockHoldersClient(); we take it directly so the
	// helpers (CountByNamedLock, ListByStoreRegion, Insert, DeleteByID,
	// UpdateAddress) that the acquisition + release paths need are
	// reachable without going through the storage adapter. Resume
	// detection moved into the substrate; the supervisor no longer
	// probes for or rebinds existing rows.
	LockHolders *store.LockHoldersClient
	// StoreRegistry is the per-process store registry built at supervisor
	// startup from stores.yml (spec §6.1). The runner dispatches against
	// the 4-verb store.Store interface (Open/Commit/Abandon/Release).
	StoreRegistry *store.Registry
	// NamedLocks is the operator-side named-lock config (limits per
	// name). The §7.3 acquisition path enforces counter-semaphore
	// semantics: under the per-name advisory lock, the unexpired
	// lock-holder count must be < limit before insert. Empty / missing
	// → no limits enforced (limit defaults to ∞).
	NamedLocks store.NamedLocksConfig

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
	StoreRegistry *store.Registry
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
	// terminal handler reads it for the policy chain (`error_types`)
	// and the quality rules (`runQualityRules`). May be nil when the
	// runner could not locate a matching def at acquisition.
	NodeDef *node.TemplateNodeDef
	// ResolvedAttributes is the post-substitution attribute map the
	// runner produced at dispatch time. The Complete branch of the
	// terminal handler merges the executor's `attributes_delta` into
	// this map and validates the result.
	ResolvedAttributes map[string]any
	// AttributesSchema is the per-node-type JSON schema fragment the
	// terminal handler validates against on a Complete with non-empty
	// delta. Source: `NodeDef.Attributes.Schema`.
	AttributesSchema map[string]any
}

// AcquiredLock bundles a lock-holder row with its originating spec and
// (for ClaimSpec) the store-returned ClaimResult. The runner builds
// one of these per successful Open call; the terminal handler walks
// the slice in deterministic sort order (blessed-invariant 3) to drive
// Commit/Abandon and the auto-terminal subgraph check.
type AcquiredLock struct {
	// Spec is one of store.NamedLockSpec or store.ClaimSpec.
	Spec any
	// LockHolderID is the rimsky_lock_holders row id created at
	// acquisition. Drives all subsequent claimant-guarded mutations.
	LockHolderID shared.UUID
	// ClaimResult is populated by Open for ClaimSpec acquisitions;
	// zero for NamedLockSpec.
	ClaimResult store.ClaimResult
	// Store is the resolved Store. Nil for NamedLockSpec; non-nil for
	// ClaimSpec.
	Store store.Store
	// Alias is the per-claim alias for ClaimSpec acquisitions; "" for
	// NamedLockSpec.
	Alias string
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

	// Step 2 (formerly OpenHandle) — retired: the substrate's Open
	// returns the address inside the acquisition tx; there is no
	// separate native-handle stage.

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
	if args.Storage == nil {
		return errors.New("supervisor.RunNode: Storage is required")
	}
	if args.Queue == nil {
		return errors.New("supervisor.RunNode: Queue is required")
	}
	if args.QueuePool == nil {
		return errors.New("supervisor.RunNode: QueuePool is required")
	}
	if args.LockHolders == nil {
		return errors.New("supervisor.RunNode: LockHolders is required")
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
// Resume detection lives in the substrate (the substrate detects
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
	prior, _ := args.Storage.NodeAttributes().Get(ctx, nodeID)
	attempt := 1
	if prior != nil {
		attempt = prior.RunAttempt + 1
	}
	return args.Storage.NodeAttributes().Upsert(ctx, nodeID, attempt, resolvedAttrs)
}

// emitTemplateResolutionFailedEvent appends the typed event for a
// dispatch-time substitution miss. Used by both the attribute-resolve
// path and the lock/region pre-substitution path.
func emitTemplateResolutionFailedEvent(
	ctx context.Context, args RunArgs, nodeID, instanceID shared.UUID, directive, site, field, reason string,
) {
	_ = args.Storage.Events().Append(ctx, storage.EventAppendInput{
		NodeID: &nodeID, InstanceID: &instanceID,
		Kind: "template_resolution_failed",
		Payload: map[string]any{
			"directive": directive,
			"site":      site,
			"field":     field,
			"reason":    reason,
		},
	}, nil)
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
	return applyTerminalAppError(ctx, args, acq, "template_resolution_failed",
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

// applyTerminalAppError funnels an application-level terminal through
// the OnError policy chain. Defined in runner_terminal.go.
