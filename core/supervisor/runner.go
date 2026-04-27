// Omnibus runner — spec §17.1.
//
// This file is the supervisor's per-claim-cycle execution path under the
// stores redesign. One call to RunNode picks an eligible candidate, runs
// the §13.3 atomic acquisition transaction (candidate selection, in-Go
// eligibility, advisory locks, claim+lock-holder inserts, store
// AcquireLock), runs the verify-before-run guard (§13.3 step 4),
// transitions the node to running (§13.3 step 4.5), opens native handles
// (§17.1 step 2), resolves attribute source-directives (§17.1 step 3),
// determines the dispatch path (§17.1 step 4), runs the heartbeat loop
// (§17.1 step 5), and applies the terminal event (§17.1 step 6).
//
// Helpers split across files for readability:
//   - runner_acquire.go  — §13.3 atomic acquisition + verify-before-run
//   - runner_dispatch.go — §17.1 step 4 dispatch path (executor / native)
//   - runner_terminal.go — §17.1 step 6 terminal-event handling
//
// @blessed-invariant 3: Multi-lock acquisition uses deterministic sorted
// order. (Spec §18 invariant 3; §13.7.) For each candidate dispatch all
// per-spec lock acquisitions (named, region, claim) are performed in
// `(lock_kind, sort_key)` order to prevent deadlock under concurrent
// contention on overlapping lock sets. The sort happens in
// runner_acquire.go's `sortLockSpecs`; the per-named-lock advisory locks
// (§13.3 step 3b), the region re-evaluation (§13.3 step 3d), and the
// store.AcquireLock + lock-holder INSERT loop (§13.3 step 3e) all walk
// the same sorted slice. Removing the sort or walking it in a different
// order in any of those steps reintroduces the deadlock the invariant
// guards against.
//
// @blessed-invariant 5: Verify-before-run. (Spec §18 invariant 5.) After
// the acquisition tx commits, the runner does a separate read of
// `rimsky_dispatch.claimed_by` and bails to the orphan-claim-lost-race
// handler if ownership has moved. The read happens in
// runner_acquire.go's `verifyBeforeRun` and is intentionally outside the
// acquisition tx — running the check inside the tx would race with
// other supervisors that also see the row as theirs because of MVCC
// snapshot isolation; the bail here is what catches the rare cross-
// transaction handoff and keeps the §17 ownership invariant intact.
//
// @blessed-invariant 10: Lock acquisition is atomic with dispatch claim.
// (Spec §18 invariant 10.) The acquisition tx in runner_acquire.go
// either claims the dispatch row AND inserts every required
// `rimsky_lock_holders` row AND completes every store `AcquireLock`
// mutation, or none of these. The whole sequence runs inside a single
// `pgx.Tx`; advisory locks released on commit, lock-holder rows visible
// on commit, store-side mutations (e.g. claim_store-postgres items-
// table flip) committed via `store.WithTx(ctx, tx)` so they share the
// same atomicity boundary. Adding a non-tx mutation between candidate
// selection and commit (or sneaking a separate connection into
// AcquireLock) breaks the invariant.
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
// pool access to drive the §13.3 transaction directly, needs the store
// registry for AcquireLock / OpenHandle / Commit / ReleaseLock dispatch,
// and needs the lock-holders client for INSERT / rebind / DELETE.
type RunArgs struct {
	// Storage is the rimsky_* table accessor surface.
	Storage storage.StorageBackend
	// Queue exposes the queue-side helpers (SelectCandidates,
	// ClaimDispatchRow, RefreshHeartbeat, …) that participate in the
	// §13.3 acquisition tx.
	Queue queue.DispatchQueue
	// QueuePool is the *pgxpool.Pool the queue is bound to. The runner
	// opens the §13.3 acquisition tx on this pool so the queue helpers,
	// the lock-holders inserts, and the per-store AcquireLock mutations
	// all share one tx. core/queue/postgres.Queue exposes Pool() to make
	// this trivial; non-postgres queue impls would need to wire their own
	// pool.
	QueuePool *pgxpool.Pool
	// LockHolders is the database-facing helper for rimsky_lock_holders.
	// Source: storage.LockHoldersClient(); we take it directly so the
	// helpers (RebindForResume, ListByNodeAndStore, CountByNamedLock,
	// ListByStoreRegion, Insert, DeleteByID, PreserveForResume) that the
	// acquisition + release paths need are reachable without going
	// through the storage adapter.
	LockHolders *store.LockHoldersClient
	// StoreRegistry is the per-process store registry built at supervisor
	// startup from stores.yml (spec §14.1). The runner type-asserts on
	// concrete capabilities (ClaimableStore, ResumableStore) per §17.2.
	StoreRegistry *store.Registry

	Clock        shared.Clock
	Logger       shared.Logger
	SupervisorID string
	// AcceptedExecutors / AcceptedStores are the supervisor pool's
	// accept-lists. Threaded into SelectCandidates' dispatch SELECT so
	// the runner only considers rows the pool can satisfy (spec §14.2).
	AcceptedExecutors []string
	AcceptedStores    []string

	Pool        *executor.ClientPool
	Resolver    executor.Resolver
	CallbackURL string

	// HeartbeatInterval drives the §13.3 step 3 lock-holder ExpiresAt
	// budget (5 × heartbeatInterval) and the in-loop heartbeat tick.
	// Zero falls back to 5s.
	HeartbeatInterval time.Duration
	// ResumeGrace is the §13.6 preserve-for-resume cutoff. Zero falls
	// back to 30 minutes.
	ResumeGrace time.Duration
	// SelectCandidatesLimit caps the dispatch SELECT batch size in the
	// §13.3 step 1 candidate read. Zero falls back to 8 — small enough
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
// state — the §12.4 callback path reconstructs both before invoking
// `applyTerminal*` so the same per-lock release tx, §5.6.4 resolution,
// state-transition, and event-emission flow runs whether the terminal
// arrived synchronously over the executor RPC or asynchronously via
// HTTP callback.
type AsyncContext struct {
	NodeID        shared.UUID
	InstanceID    shared.UUID
	DispatchID    shared.UUID
	SupervisorID  string
	StoreRegistry *store.Registry
	// FrameID is the dispatch row's frame_id — propagated through async
	// handoff so the terminal handler can re-enqueue retries with the
	// correct frame_id (per spec §10.2 / blessed-invariant 19).
	FrameID shared.UUID
	// AcquiredLocks is the set of lock-holder rows the runner inserted
	// during acquisition. The terminal handler walks these in §13.7
	// sort order to drive Commit + ReleaseLock + §5.6.4 resolution.
	AcquiredLocks []AcquiredLock
	// NodeType is the candidate's template node type. Used by the
	// terminal handler when classifying §12.6 actions and when
	// re-enqueueing on retry / infra_reenqueue.
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
	// runner produced at dispatch time (§17.1 step 3). The Complete
	// branch of the terminal handler merges the executor's
	// `attributes_delta` into this map and validates the result.
	ResolvedAttributes map[string]any
	// AttributesSchema is the per-node-type JSON schema fragment the
	// terminal handler validates against on a Complete with non-empty
	// delta. Source: `NodeDef.Attributes.Schema`.
	AttributesSchema map[string]any
}

// AcquiredLock bundles a lock-holder row with its originating spec and
// the store-returned ClaimResult. The runner builds one of these per
// successful (or rebound) AcquireLock call; all subsequent operations
// (OpenHandle, Commit, ReleaseLock, §5.6.4 resolution) work off this
// shape.
type AcquiredLock struct {
	Spec        store.LockSpec
	Handle      store.LockHandle
	ClaimResult store.ClaimResult
	// Resumed is true when the rebind path (§13.3 step 3a) found a prior
	// lock-holder row owned by this supervisor and reused it.
	Resumed bool
	// Store is the resolved Store. Cached so the terminal handler does
	// not need to re-look-up the registry.
	Store store.Store
	// Native is populated by §17.1 step 2's OpenHandle call.
	Native store.NativeHandle
}

// RunNode runs one full claim-and-execute cycle. The eight-stage outline
// of §17.1 is laid out here as a tall function with named helpers; each
// stage either succeeds and continues or bails to a recoverable handler.
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

	// Step 1 — §13.3 atomic acquisition + verify-before-run + state
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

	// Step 2 — open native handles.
	if err := openNativeHandles(ctx, &acq); err != nil {
		// OpenHandle failed mid-flight. Treat as infra error and route
		// through the give-up branch of the terminal handler so the
		// store side is reset cleanly.
		return RunnerResult{Ran: true, NodeID: acq.NodeID, DispatchID: acq.DispatchID}, applyTerminalGiveUp(ctx, args, &acq, "open_handle_failed", map[string]any{"error": err.Error()})
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
	//
	// Per spec §5.7.3, on resume_then_retry (any lock rebound, Resumed=true)
	// the executor-populated fields are preserved verbatim and only
	// source-driven fields are repopulated. On retry without resume the
	// row is replaced outright (executor-populated fields cleared).
	resumed := false
	for _, lk := range acq.Locks {
		if lk.Resumed {
			resumed = true
			break
		}
	}
	if err := upsertAttributesPreDispatch(ctx, args, acq.NodeID, resolvedAttrs, attrSchema, resumed); err != nil {
		log.Warn("runner: upsert attributes pre-dispatch failed",
			"node_id", acq.NodeID.String(), "error", err.Error())
	}

	// Per spec §5.7.3, the executor receives the full attributes row
	// (source-driven repopulated + preserved executor-populated). When
	// not resuming, the row is just `resolvedAttrs`. When resuming, the
	// freshly persisted row carries the merged shape — re-read it so
	// downstream paths (dispatch + terminal handling) see the same view.
	dispatchAttrs := resolvedAttrs
	if resumed {
		if persisted, _ := args.Storage.NodeAttributes().Get(ctx, acq.NodeID); persisted != nil && len(persisted.Data) > 0 {
			dispatchAttrs = persisted.Data
		}
	}

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
// Source-driven vs. executor-populated provenance is determined from the
// schema: a property declaration with a `source:` directive is
// source-driven; a property declaration without `source:` is
// executor-populated. (Spec §5.7.)
//
// When `resumed` is true (resume_then_retry path), executor-populated
// fields from the prior row are preserved verbatim and merged with the
// freshly substituted source-driven fields. When `resumed` is false
// (initial dispatch or discard_then_retry path), the row is replaced
// outright so executor-populated fields are cleared.
func upsertAttributesPreDispatch(
	ctx context.Context,
	args RunArgs,
	nodeID shared.UUID,
	resolvedAttrs map[string]any,
	schema map[string]any,
	resumed bool,
) error {
	prior, _ := args.Storage.NodeAttributes().Get(ctx, nodeID)
	attempt := 1
	if prior != nil {
		attempt = prior.RunAttempt + 1
	}
	data := resolvedAttrs
	if resumed && prior != nil && len(prior.Data) > 0 {
		data = mergePreserveExecutorPopulated(prior.Data, resolvedAttrs, schema)
	}
	return args.Storage.NodeAttributes().Upsert(ctx, nodeID, attempt, data)
}

// mergePreserveExecutorPopulated merges the prior row's executor-populated
// fields (schema properties without a `source:` directive) into the
// freshly substituted source-driven map. Source-driven fields always come
// from `resolved`; executor-populated fields fall through from `prior`
// unchanged.
func mergePreserveExecutorPopulated(prior, resolved, schema map[string]any) map[string]any {
	out := make(map[string]any, len(prior)+len(resolved))
	executorPopulated := executorPopulatedFields(schema)
	for k, v := range prior {
		if _, ok := executorPopulated[k]; ok {
			out[k] = v
		}
	}
	for k, v := range resolved {
		out[k] = v
	}
	return out
}

// executorPopulatedFields returns the set of attribute property names
// that are executor-populated (i.e. declared in the schema without a
// `source:` directive). Per spec §5.7.
func executorPopulatedFields(schema map[string]any) map[string]struct{} {
	out := map[string]struct{}{}
	if schema == nil {
		return out
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return out
	}
	for name, propAny := range props {
		prop, _ := propAny.(map[string]any)
		if prop == nil {
			continue
		}
		if _, hasSource := prop["source"]; hasSource {
			continue
		}
		out[name] = struct{}{}
	}
	return out
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

// applyTerminalGiveUp is the convenience wrapper used when an error
// surfaces between the acquisition commit and the executor RPC; mirrors
// the §17.1 step 6 give-up policy chain branch.
func applyTerminalGiveUp(ctx context.Context, args RunArgs, acq *acquisition, errClass string, payload map[string]any) error {
	return applyTerminalAppError(ctx, args, acq, errClass, payload)
}

// applyTerminalAppError funnels an application-level terminal through
// the OnError policy chain. Defined in runner_terminal.go.
