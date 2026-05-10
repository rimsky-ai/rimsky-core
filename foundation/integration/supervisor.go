// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Supervisor main loop. Spec §7.3 (per-dispatch flow) + §4.10
// invariants for the ownership story. Supervisor:
//
//   - Registers self in rimsky_supervisors (callback host/port, accepted
//     executors, accepted stores, concurrency).
//   - Starts an HTTP callback server so async-handoff executors can POST
//     terminal outcomes (see callback.go).
//   - Loops:
//   - Heartbeat tick (HeartbeatInterval): update own supervisor row, update
//     per-active-node heartbeats, refresh `rimsky_claim_handle` rows owned
//     by this supervisor whose holder_node_id is currently `running` (per
//     spec §7.5). Operator invalidates do not preempt running work — they
//     enqueue/coalesce a frame (per
//     docs/history/2026-04-26-frame-resolution-design.md §3.3 / §5.4).
//   - Claim tick (ClaimPollInterval): while active < concurrency, try to
//     claim one dispatch row via queue.Claim; on success, dispatch RunNode
//     in a goroutine.
//   - On shutdown: stop claiming, wait up to 30s for active runs, close the
//     callback server, unregister, close the executor client pool.
//
// @blessed-invariant 4: Claimant-guarded release. (Spec §4.10 invariant 4.)
//
//	Every DELETE FROM rimsky_claim_handle and every UPDATE
//	rimsky_worker_request SET claimed_by = NULL is gated on
//	`AND … = supervisor_id`. The §7.5 heartbeat refresh below is a
//	WRITE (not a release), but it inherits the same claimant guard:
//	the WHERE clause is `holder_supervisor_id = $1`, so a stale
//	heartbeat from a different supervisor can never extend a row it
//	doesn't own. Concrete enforcement of the release path lives
//	across persistence.Queue / persistence.ClaimHandlesStore impls,
//	`foundation/integration/runner.go`, and `modeling/scheduler/scheduler.go`;
//	this file's contribution is the heartbeat-write claimant guard.
//	Do not relax the `holder_supervisor_id = $1` predicate on the
//	heartbeat UPDATE.
//
// @blessed-invariant 10: Lock acquisition is atomic with dispatch
// claim (rimsky-side). Per v3 spec §4.10:
//
//	The §7.3 acquisition transaction either claims the dispatch row
//	AND inserts every required `rimsky_claim_handle` row AND records
//	the `Store.Open`-returned address, or none of these. The store's
//	own state mutations run in a store-internal transaction
//	decoupled from rimsky's. Single-writer-per-scope (invariant 4b)
//	holds because rimsky's conflict predicate gates lock-holder
//	INSERTs against `rimsky_claim_handle` only. The acquisition tx
//	lives in `foundation/integration/runner_acquire.go`; this file's
//	contribution is structural — the heartbeat refresh below MUST
//	NOT extend rows for nodes that have transitioned out of
//	`running`. The `holder_node_id IN (running-nodes)` filter keeps
//	the orphan-reap cutoff (5 × heartbeat_interval) attainable so
//	stranded held subgraphs are eventually reaped.
package integration

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/executor"
	"github.com/fallguy/rimsky/modeling/shared"
)

// Config is the supervisor's construction-time dependency bundle.
type Config struct {
	SupervisorID string
	// Persist is the persistence.Store handle (rimsky_* tables). Required.
	Persist persistence.Store
	// Queue is the dispatch-queue accessor. Required.
	Queue persistence.Queue
	// AdvisoryLocker carries advisory-lock primitives the acquisition tx uses.
	// Required.
	AdvisoryLocker    persistence.AdvisoryLocker
	Clock             shared.Clock
	Logger            shared.Logger
	Concurrency       int
	HeartbeatInterval time.Duration // default 5s
	ClaimPollInterval time.Duration // default 1s
	Resolver          executor.Resolver
	// StoreRegistry is the per-process store registry built by
	// config.StartSupervisor from the YAML stores config (spec §6.1).
	// The supervisor uses it for: deriving `accepted_stores` at
	// registration (§6.2), and looking up concrete *locks.Store values
	// during dispatch (§7.3) and commit (§7.6). Required (Start
	// returns an error when nil).
	StoreRegistry *locks.Registry
	// NamedLocks is the operator-side named-lock config (limits per
	// name). The supervisor enforces counter-semaphore semantics at
	// acquire time: under the per-name advisory lock, the unexpired
	// lock-holder count must be < limit before insert. Empty / missing
	// → no limits enforced; templates referencing any named lock will
	// have failed validation at deploy time (control-api wires the
	// hook).
	NamedLocks   locks.NamedLocksConfig
	CallbackHost string
	CallbackPort int
	// CallbackAdvertiseHost / CallbackAdvertisePort override the host:port the
	// supervisor *advertises* to executors for async-handoff callbacks. Use
	// these when the listener bind address (e.g. `0.0.0.0:9100` in a
	// container) differs from how peers can reach the supervisor (e.g.
	// `rimsky-supervisor:9100` over the docker-compose network). Both empty /
	// zero → fall back to the listener addr. If only the host is set the port
	// falls back to the listener's bound port.
	CallbackAdvertiseHost string
	CallbackAdvertisePort int

	// Blob is the active BlobBackend (loaded from BlobConfig at startup,
	// already installed on the persistence driver via SetBlobBackend).
	// Threaded into RunArgs so the named-event and parked-payload write
	// paths can spill payloads through it. Nil disables spill at those
	// sites (the persistence-layer attribute write path consults the
	// same backend via the driver's storeImpl).
	Blob persistence.BlobBackend
	// BlobSpillThreshold is the spill cutoff in bytes. Zero disables spill.
	BlobSpillThreshold int
	// MaxRetriesWithoutProgressDefault is the deployment-level cap on
	// consecutive retries with no last_outcome change. Threaded into both
	// the synchronous RunArgs (built per tryClaim) and the async-callback
	// CallbackServer so async-callback-driven retries observe the same cap.
	MaxRetriesWithoutProgressDefault int
	// UserdataValidator is the dispatch-time userdata schema validator
	// (plan F7). Threaded into RunArgs so buildExecuteRequest can run
	// it after applyUserdataOverrides. Failures route through the
	// on_executor_errored chain with error_class="userdata_validation_failed".
	// Nil → validation skipped (used in unit tests).
	UserdataValidator func(executorName string, merged map[string]any) error
	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook (plan I1/I2/I3). Optional; nil → no-op everywhere.
	Metrics MetricsHook
}

// Handle is returned by Start. Callers drive lifecycle via Shutdown and
// inspect the callback endpoint via CallbackAddr.
type Handle struct {
	stop          chan struct{}
	done          chan struct{}
	addr          string
	advertisedURL string
	// wg tracks in-flight run goroutines spawned by tryClaim so Shutdown can
	// wait on their completion without a polling loop.
	wg sync.WaitGroup
}

// Shutdown signals the main loop to stop claiming, waits for active runs to
// drain, closes the callback server, and unregisters the supervisor.
func (h *Handle) Shutdown(ctx context.Context) error {
	select {
	case <-h.stop:
		// already stopped
	default:
		close(h.stop)
	}
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CallbackAddr returns the bound host:port of the callback server.
func (h *Handle) CallbackAddr() string { return h.addr }

// Start launches the supervisor main loop. Returns once the callback server
// is listening and the supervisor is registered; the claim/heartbeat loop
// runs on a background goroutine.
func Start(cfg Config) (*Handle, error) {
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.ClaimPollInterval == 0 {
		cfg.ClaimPollInterval = 1 * time.Second
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Logger == nil {
		cfg.Logger = shared.SilentLogger{}
	}
	if cfg.Clock == nil {
		cfg.Clock = shared.SystemClock{}
	}
	if cfg.Persist == nil || cfg.Queue == nil || cfg.Resolver == nil {
		return nil, errors.New("supervisor.Start: Persist, Queue, and Resolver are required")
	}
	if cfg.AdvisoryLocker == nil {
		return nil, errors.New("supervisor.Start: AdvisoryLocker is required")
	}
	if cfg.StoreRegistry == nil {
		return nil, errors.New("supervisor.Start: StoreRegistry is required")
	}

	lockHolders := cfg.Persist.ClaimHandles()

	callbackReg := NewCallbackRegistry()
	// Build the unified-invalidate adapter once and share it between the
	// sync path (per-tryClaim RunArgs below) and the async-callback path
	// (CallbackServer). Without this, handler-emitted invalidates that
	// arrive via async callback fall back to bare InvalidateNode and
	// cannot wake parked targets through the H2 unified path.
	//
	// Metrics threaded through here so InvalidateNode's
	// `rimsky_invalidates_total` counter increments at every async-
	// callback-driven invalidate. Callers that already populated
	// ia.Metrics (e.g. handler emits that built their own InvalidateArgs)
	// keep their value; otherwise we fall back to cfg.Metrics.
	invalidateAdapter := func(ctx context.Context, ia InvalidateArgs) error {
		if ia.Metrics == nil {
			ia.Metrics = cfg.Metrics
		}
		return UnifiedInvalidate(ctx, ia, cfg.SupervisorID, WakeExternalInvalidate)
	}
	callbackSrv := &CallbackServer{
		Registry:                         callbackReg,
		Persist:                          cfg.Persist,
		Queue:                            cfg.Queue,
		AdvisoryLocker:                   cfg.AdvisoryLocker,
		ClaimHandles:                     lockHolders,
		Clock:                            cfg.Clock,
		Logger:                           cfg.Logger,
		SupervisorID:                     cfg.SupervisorID,
		Blob:                             cfg.Blob,
		BlobSpillThreshold:               cfg.BlobSpillThreshold,
		InvalidateHandler:                invalidateAdapter,
		MaxRetriesWithoutProgressDefault: cfg.MaxRetriesWithoutProgressDefault,
		UserdataValidator:                cfg.UserdataValidator,
		Metrics:                          cfg.Metrics,
	}
	addr, err := callbackSrv.Start(cfg.CallbackHost, cfg.CallbackPort)
	if err != nil {
		return nil, err
	}

	// Parse host:port from addr for registration in rimsky_supervisors.
	host, port := splitHostPort(addr)
	accepted := cfg.Resolver.AcceptedNames()
	acceptedStores := storeRegistryNames(cfg.StoreRegistry)
	if err := cfg.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return cfg.Persist.Supervisors().Register(ctx, persistence.SupervisorRegisterInput{
			ID:                cfg.SupervisorID,
			AcceptedExecutors: accepted,
			AcceptedStores:    acceptedStores,
			Concurrency:       cfg.Concurrency,
			CallbackHost:      host,
			CallbackPort:      port,
		}, tx)
	}); err != nil {
		_ = callbackSrv.Close(context.Background())
		return nil, err
	}

	clientPool := executor.NewClientPool()
	advertised := advertisedCallbackURL(addr, cfg.CallbackAdvertiseHost, cfg.CallbackAdvertisePort)
	h := &Handle{stop: make(chan struct{}), done: make(chan struct{}), addr: addr, advertisedURL: advertised}
	go runLoop(cfg, h, callbackSrv, callbackReg, clientPool, accepted, acceptedStores, lockHolders)
	return h, nil
}

// storeRegistryNames returns a sorted-stable copy of the registry's store
// names. Used at registration time to populate `accepted_stores` per spec
// §6.2 (operator-declared store set the supervisor pool can satisfy).
func storeRegistryNames(reg *locks.Registry) []string {
	stores := reg.Stores()
	if len(stores) == 0 {
		return nil
	}
	out := make([]string, 0, len(stores))
	for name := range stores {
		out = append(out, name)
	}
	return out
}

// advertisedCallbackURL computes the base URL the supervisor hands to
// executors as `callback_url`. Preference order: (advertiseHost,
// advertisePort) → (advertiseHost, listener port) → listener addr.
func advertisedCallbackURL(listenerAddr, advertiseHost string, advertisePort int) string {
	if advertiseHost == "" {
		return "http://" + listenerAddr
	}
	port := advertisePort
	if port == 0 {
		_, lp := splitHostPort(listenerAddr)
		port = lp
	}
	return "http://" + net.JoinHostPort(advertiseHost, strconv.Itoa(port))
}

// runLoop is the main claim/heartbeat loop. Exits when h.stop closes.
func runLoop(
	cfg Config,
	h *Handle,
	srv *CallbackServer,
	reg *CallbackRegistry,
	pool *executor.ClientPool,
	accepted []string,
	acceptedStores []string,
	lockHolders persistence.ClaimHandlesStore,
) {
	defer close(h.done)
	cfg.Logger.Info("supervisor started",
		"supervisor_id", cfg.SupervisorID,
		"accepts", accepted,
		"concurrency", cfg.Concurrency)

	// activeCount tracks the supervisor's in-flight tryClaim goroutines
	// (incremented before launch, decremented when the goroutine exits).
	// Used for the concurrency guard below and the shutdown drain wait;
	// NOT used for the supervisor's `active_node_count` heartbeat field —
	// that comes from a DB query so async dispatches whose goroutines
	// have already returned (post-AsyncAccepted) are still counted.
	// See doHeartbeat below.
	var activeMu sync.Mutex
	activeCount := 0

	heartbeatTick := time.NewTicker(cfg.HeartbeatInterval)
	defer heartbeatTick.Stop()
	claimTick := time.NewTicker(cfg.ClaimPollInterval)
	defer claimTick.Stop()

	doHeartbeat := func() {
		hbCtx := context.Background()

		// DB-driven view of "what this supervisor is currently running."
		// The result of this query drives both the per-node
		// last_heartbeat_at refresh below AND the active_node_count we
		// stamp into rimsky_supervisors via Supervisors().Heartbeat.
		// Both must reflect the same source of truth — the in-memory
		// goroutine counter (`activeCount`, retained for the concurrency
		// guard below) under-counts async dispatches whose RunNode
		// goroutine returned at AsyncAccepted while the actual work
		// continues on the executor side. Reading the DB here covers
		// sync + async + any future "RunNode returned early" path.
		var running []persistence.NodeRow
		if err := cfg.Persist.Transaction(hbCtx, func(ctx context.Context, tx persistence.Tx) error {
			rows, err := cfg.Persist.Nodes().ListRunningBySupervisor(ctx, cfg.SupervisorID, tx)
			running = rows
			return err
		}); err != nil {
			cfg.Logger.Warn("supervisor: list running nodes by supervisor failed", "error", err.Error())
			// Fall through to the supervisor + lock-holder heartbeats with a
			// zero count — keeping THIS supervisor's row alive matters more
			// than the running-nodes count being momentarily wrong.
			running = nil
		}

		if err := cfg.Persist.Transaction(hbCtx, func(ctx context.Context, tx persistence.Tx) error {
			return cfg.Persist.Supervisors().Heartbeat(ctx, cfg.SupervisorID, len(running), tx)
		}); err != nil {
			cfg.Logger.Warn("supervisor: supervisors.Heartbeat failed", "error", err.Error())
		}
		// §7.5 lock-holder heartbeat refresh. ExtendHeartbeat issues the
		// SQL with the running-nodes subquery filter so preserve-for-resume
		// rows (anchored to nodes that have transitioned out of `running`
		// to `stale`) are NOT refreshed — the resume-grace cutoff
		// would never fire otherwise. The expiresAt budget is `5 ×
		// HeartbeatInterval` per the spec; the persistence layer converts
		// the duration back to integer seconds for the §7.5 SQL literal.
		expiresAt := cfg.Clock.Now().Add(5 * cfg.HeartbeatInterval)
		if err := cfg.Persist.Transaction(hbCtx, func(ctx context.Context, tx persistence.Tx) error {
			return lockHolders.ExtendHeartbeat(ctx, cfg.SupervisorID, expiresAt, tx)
		}); err != nil {
			cfg.Logger.Warn("supervisor: lockHolders.ExtendHeartbeat failed", "error", err.Error())
		}
		now := cfg.Clock.Now()
		for _, n := range running {
			nodeID := n.ID
			if err := cfg.Persist.Transaction(hbCtx, func(ctx context.Context, tx persistence.Tx) error {
				return cfg.Persist.Nodes().UpdateHeartbeat(ctx, nodeID, now, cfg.SupervisorID, tx)
			}); err != nil {
				cfg.Logger.Warn("supervisor: node UpdateHeartbeat failed",
					"node_id", nodeID.String(), "error", err.Error())
			}
		}
	}

	tryClaim := func() {
		activeMu.Lock()
		if activeCount >= cfg.Concurrency {
			activeMu.Unlock()
			return
		}
		// Reserve a slot before launching. RunNode does its own §7.3
		// candidate selection; if there's no eligible candidate it bails
		// quickly and we release the slot in the defer below.
		activeCount++
		activeMu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer cancel()
			// Build the unified-invalidate adapter on the supervisor's
			// own persistence handle so handler-emitted invalidates (H2
			// on_event) wake parked targets through the same path as
			// the admin endpoint (G3) and the parked-node sweep (E3).
			// Metrics fallback matches the async-callback adapter above.
			invalidateAdapter := func(ctx context.Context, ia InvalidateArgs) error {
				if ia.Metrics == nil {
					ia.Metrics = cfg.Metrics
				}
				return UnifiedInvalidate(ctx, ia, cfg.SupervisorID, WakeExternalInvalidate)
			}
			result, runErr := RunNode(ctx, RunArgs{
				Persist:                          cfg.Persist,
				Queue:                            cfg.Queue,
				AdvisoryLocker:                   cfg.AdvisoryLocker,
				ClaimHandles:                     lockHolders,
				Clock:                            cfg.Clock,
				Logger:                           cfg.Logger,
				SupervisorID:                     cfg.SupervisorID,
				AcceptedExecutors:                accepted,
				AcceptedStores:                   acceptedStores,
				Pool:                             pool,
				Resolver:                         cfg.Resolver,
				StoreRegistry:                    cfg.StoreRegistry,
				NamedLocks:                       cfg.NamedLocks,
				CallbackURL:                      h.advertisedURL,
				HeartbeatInterval:                cfg.HeartbeatInterval,
				Blob:                             cfg.Blob,
				BlobSpillThreshold:               cfg.BlobSpillThreshold,
				InvalidateHandler:                invalidateAdapter,
				MaxRetriesWithoutProgressDefault: cfg.MaxRetriesWithoutProgressDefault,
				UserdataValidator:                cfg.UserdataValidator,
				Metrics:                          cfg.Metrics,
			}, reg.Register)
			if runErr != nil {
				cfg.Logger.Warn("supervisor: RunNode failed", "error", runErr.Error())
			}

			// Per-node heartbeat tracking is DB-driven (see doHeartbeat
			// above) — no in-memory bookkeeping of result.NodeID is
			// needed here. The heartbeat tick reads
			// `rimsky_nodes WHERE state='running' AND assigned_supervisor_id=$self`
			// directly, which is correct for both sync (RunNode in-flight)
			// and async (handed off, still running in the DB) dispatches.

			// Release the reserved slot.
			defer func() {
				activeMu.Lock()
				activeCount--
				activeMu.Unlock()
			}()

			// Async path: the callback endpoint owns dispatch cleanup.
			if result.Async {
				return
			}
			// result.Ran covers both success and app/infra-error paths.
			// For the infra case applyTerminalInfraError has already
			// enqueued a fresh dispatch row; the Complete below deletes
			// the original row we claimed. For resolver-miss the node
			// transitioned directly to failed without enqueueing a retry,
			// so deleting the original row is also correct.
			if result.Ran && result.DispatchID != (shared.UUID{}) {
				_ = cfg.Queue.Complete(context.Background(), result.DispatchID, cfg.SupervisorID)
			}
		}()
	}

	for {
		select {
		case <-h.stop:
			// Shutdown: stop claiming and wait up to 30s for active runs to
			// drain via wg. Using a WaitGroup (incremented inside tryClaim
			// when a run goroutine launches, decremented on its defer)
			// replaces the old 50ms polling loop.
			waitCtx, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
			waitDone := make(chan struct{})
			go func() {
				h.wg.Wait()
				close(waitDone)
			}()
			select {
			case <-waitDone:
			case <-waitCtx.Done():
				activeMu.Lock()
				remaining := activeCount
				activeMu.Unlock()
				if remaining > 0 {
					cfg.Logger.Warn("supervisor shutdown timed out with active runs",
						"active", remaining)
				}
			}
			cancelWait()
			_ = srv.Close(context.Background())
			if err := cfg.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
				return cfg.Persist.Supervisors().Unregister(ctx, cfg.SupervisorID, tx)
			}); err != nil {
				cfg.Logger.Warn("supervisor: Unregister failed", "error", err.Error())
			}
			_ = pool.Close()
			cfg.Logger.Info("supervisor stopped", "supervisor_id", cfg.SupervisorID)
			return
		case <-heartbeatTick.C:
			doHeartbeat()
		case <-claimTick.C:
			tryClaim()
		}
	}
}

// splitHostPort parses "host:port" (or "[ipv6]:port") into (host, portInt).
// Returns ("", 0) on parse failure.
func splitHostPort(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0
	}
	p, _ := strconv.Atoi(portStr)
	return host, p
}
