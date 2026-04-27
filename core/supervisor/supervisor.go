// Plan A Task 10.4 — supervisor main loop.
//
// Port of rimsky/src/supervisor/supervisor.ts. Spec §6.2 (per-dispatch flow),
// §17 (ownership @blessed-invariant). Supervisor:
//
//   - Registers self in rimsky_supervisors (callback host/port, accepted
//     executors, accepted stores, concurrency).
//   - Starts an HTTP callback server so async-handoff executors can POST
//     terminal outcomes (see callback.go).
//   - Loops:
//   - Heartbeat tick (HeartbeatInterval): update own supervisor row, update
//     per-active-node heartbeats, refresh `rimsky_lock_holders` rows owned
//     by this supervisor whose holder_node_id is currently `running` (per
//     spec §13.4). Operator invalidates do not preempt running work — they
//     enqueue/coalesce a frame (per
//     docs/specs/2026-04-26-frame-resolution-design.md §3.3 / §5.4).
//   - Claim tick (ClaimPollInterval): while active < concurrency, try to
//     claim one dispatch row via queue.Claim; on success, dispatch RunNode
//     in a goroutine.
//   - On shutdown: stop claiming, wait up to 30s for active runs, close the
//     callback server, unregister, close the executor client pool.
//
// @blessed-invariant 4: Claimant-guarded release. (Spec §18 invariant 4.)
//
//	Every DELETE FROM rimsky_lock_holders and every UPDATE
//	rimsky_dispatch SET claimed_by = NULL is gated on
//	`AND … = supervisor_id`. The §13.4 heartbeat refresh below is a
//	WRITE (not a release), but it inherits the same claimant guard:
//	the WHERE clause is `holder_supervisor_id = $1`, so a stale
//	heartbeat from a different supervisor can never extend a row it
//	doesn't own. Concrete enforcement of the release path lives
//	across `core/queue/postgres/queue.go`, `core/supervisor/runner.go`,
//	and `core/scheduler/scheduler.go`; this file's contribution is the
//	heartbeat-write claimant guard. Do not relax the
//	`holder_supervisor_id = $1` predicate on the heartbeat UPDATE.
//
// @blessed-invariant 10: Lock acquisition is atomic with dispatch claim.
// (Spec §18 invariant 10.)
//
//	The §13.3 acquisition transaction either claims the dispatch row
//	AND inserts every required `rimsky_lock_holders` row AND completes
//	every Store.Open mutation, or none of these. The acquisition tx
//	itself lives in `core/supervisor/runner_acquire.go` and
//	`core/queue/postgres/queue.go` (building-block helpers); this
//	file's contribution is structural — the heartbeat refresh below
//	MUST NOT extend rows for nodes that have transitioned out of
//	`running`. The `holder_node_id IN (running-nodes)` filter is the
//	§13.4 correctness predicate that keeps held-claim subgraph
//	timeout reachable: once the acquirer leaves `running`, the
//	heartbeat refresh stops touching the row and the orphan-reap
//	cutoff (5 × heartbeat_interval) becomes attainable so stranded
//	held subgraphs are eventually reaped.
package supervisor

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

// Config is the supervisor's construction-time dependency bundle.
type Config struct {
	SupervisorID      string
	Storage           storage.StorageBackend
	Queue             queue.DispatchQueue
	Clock             shared.Clock
	Logger            shared.Logger
	Concurrency       int
	HeartbeatInterval time.Duration // default 5s
	ClaimPollInterval time.Duration // default 1s
	Resolver          executor.Resolver
	// StoreRegistry is the per-process store registry built by
	// config.StartSupervisor from the YAML stores config (spec §14.1).
	// The supervisor uses it for: deriving `accepted_stores` at
	// registration (§14.2), and looking up concrete *store.Store values
	// during dispatch (§13.3) and commit (§13.6). Required (Start
	// returns an error when nil).
	StoreRegistry *store.Registry
	CallbackHost  string
	CallbackPort  int
	// CallbackAdvertiseHost / CallbackAdvertisePort override the host:port the
	// supervisor *advertises* to executors for async-handoff callbacks. Use
	// these when the listener bind address (e.g. `0.0.0.0:9100` in a
	// container) differs from how peers can reach the supervisor (e.g.
	// `rimsky-supervisor:9100` over the docker-compose network). Both empty /
	// zero → fall back to the listener addr. If only the host is set the port
	// falls back to the listener's bound port.
	CallbackAdvertiseHost string
	CallbackAdvertisePort int
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
	if cfg.Storage == nil || cfg.Queue == nil || cfg.Resolver == nil {
		return nil, errors.New("supervisor.Start: Storage, Queue, and Resolver are required")
	}
	if cfg.StoreRegistry == nil {
		return nil, errors.New("supervisor.Start: StoreRegistry is required")
	}

	// The omnibus runner (§17.1) and the §17.1 step 6c release tx both
	// run on a *pgxpool.Pool. Type-assert on the queue impl for Pool();
	// every production queue is core/queue/postgres which exposes it.
	qpool, err := queuePool(cfg.Queue)
	if err != nil {
		return nil, err
	}
	lockHolders := store.NewLockHoldersClient(qpool)

	callbackReg := NewCallbackRegistry()
	callbackSrv := &CallbackServer{
		Registry:     callbackReg,
		Storage:      cfg.Storage,
		Queue:        cfg.Queue,
		QueuePool:    qpool,
		LockHolders:  lockHolders,
		Clock:        cfg.Clock,
		Logger:       cfg.Logger,
		SupervisorID: cfg.SupervisorID,
	}
	addr, err := callbackSrv.Start(cfg.CallbackHost, cfg.CallbackPort)
	if err != nil {
		return nil, err
	}

	// Parse host:port from addr for registration in rimsky_supervisors.
	host, port := splitHostPort(addr)
	accepted := cfg.Resolver.AcceptedNames()
	acceptedStores := storeRegistryNames(cfg.StoreRegistry)
	if err := cfg.Storage.Supervisors().Register(context.Background(), storage.SupervisorRegisterInput{
		ID:                cfg.SupervisorID,
		AcceptedExecutors: accepted,
		AcceptedStores:    acceptedStores,
		Concurrency:       cfg.Concurrency,
		CallbackHost:      host,
		CallbackPort:      port,
	}, nil); err != nil {
		_ = callbackSrv.Close(context.Background())
		return nil, err
	}

	clientPool := executor.NewClientPool()
	advertised := advertisedCallbackURL(addr, cfg.CallbackAdvertiseHost, cfg.CallbackAdvertisePort)
	h := &Handle{stop: make(chan struct{}), done: make(chan struct{}), addr: addr, advertisedURL: advertised}
	go runLoop(cfg, h, callbackSrv, callbackReg, clientPool, accepted, acceptedStores, qpool, lockHolders)
	return h, nil
}

// queuePool extracts the underlying *pgxpool.Pool from a DispatchQueue
// implementation. The omnibus runner needs a concrete pool to run §13.3
// transactions; abstracting through a pool-less interface would force
// every queue impl to expose the same — for v1 only the postgres impl
// exists, so we type-assert here.
func queuePool(q queue.DispatchQueue) (*pgxpool.Pool, error) {
	pp, ok := q.(interface{ Pool() *pgxpool.Pool })
	if !ok {
		return nil, errors.New("supervisor.Start: Queue must expose Pool() *pgxpool.Pool")
	}
	return pp.Pool(), nil
}

// storeRegistryNames returns a sorted-stable copy of the registry's store
// names. Used at registration time to populate `accepted_stores` per spec
// §14.2 (operator-declared store set the supervisor pool can satisfy).
func storeRegistryNames(reg *store.Registry) []string {
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
	qpool *pgxpool.Pool,
	lockHolders *store.LockHoldersClient,
) {
	defer close(h.done)
	cfg.Logger.Info("supervisor started",
		"supervisor_id", cfg.SupervisorID,
		"accepts", accepted,
		"concurrency", cfg.Concurrency)

	var activeMu sync.Mutex
	activeNodes := map[shared.UUID]struct{}{}
	activeCount := 0

	heartbeatTick := time.NewTicker(cfg.HeartbeatInterval)
	defer heartbeatTick.Stop()
	claimTick := time.NewTicker(cfg.ClaimPollInterval)
	defer claimTick.Stop()

	doHeartbeat := func() {
		activeMu.Lock()
		cnt := activeCount
		ids := make([]shared.UUID, 0, len(activeNodes))
		for id := range activeNodes {
			ids = append(ids, id)
		}
		activeMu.Unlock()

		if err := cfg.Storage.Supervisors().Heartbeat(context.Background(), cfg.SupervisorID, cnt, nil); err != nil {
			cfg.Logger.Warn("supervisor: supervisors.Heartbeat failed", "error", err.Error())
		}
		// §13.4 lock-holder heartbeat refresh. ExtendHeartbeat issues the
		// SQL with the running-nodes subquery filter so preserve-for-resume
		// rows (anchored to nodes that have transitioned out of `running`
		// to `stale`) are NOT refreshed — the resume-grace cutoff (§13.6)
		// would never fire otherwise. The expiresAt budget is `5 ×
		// HeartbeatInterval` per the spec; the storage adapter converts
		// the duration back to integer seconds for the §13.4 SQL literal.
		expiresAt := cfg.Clock.Now().Add(5 * cfg.HeartbeatInterval)
		if err := cfg.Storage.LockHolders().ExtendHeartbeat(context.Background(), cfg.SupervisorID, expiresAt, nil); err != nil {
			cfg.Logger.Warn("supervisor: lockHolders.ExtendHeartbeat failed", "error", err.Error())
		}
		for _, id := range ids {
			_ = cfg.Storage.Nodes().UpdateHeartbeat(context.Background(), id, cfg.Clock.Now(), cfg.SupervisorID, nil)
		}
	}

	tryClaim := func() {
		activeMu.Lock()
		if activeCount >= cfg.Concurrency {
			activeMu.Unlock()
			return
		}
		// Reserve a slot before launching. RunNode does its own §13.3
		// candidate selection; if there's no eligible candidate it bails
		// quickly and we release the slot in the defer below.
		activeCount++
		activeMu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer cancel()
			result, runErr := RunNode(ctx, RunArgs{
				Storage:           cfg.Storage,
				Queue:             cfg.Queue,
				QueuePool:         qpool,
				LockHolders:       lockHolders,
				Clock:             cfg.Clock,
				Logger:            cfg.Logger,
				SupervisorID:      cfg.SupervisorID,
				AcceptedExecutors: accepted,
				AcceptedStores:    acceptedStores,
				Pool:              pool,
				Resolver:          cfg.Resolver,
				StoreRegistry:     cfg.StoreRegistry,
				CallbackURL:       h.advertisedURL,
				HeartbeatInterval: cfg.HeartbeatInterval,
			}, reg.Register)
			if runErr != nil {
				cfg.Logger.Warn("supervisor: RunNode failed", "error", runErr.Error())
			}

			// Track active node id so the heartbeat tick can refresh
			// `rimsky_nodes.last_heartbeat_at` for it. Operator
			// invalidates do not preempt — they enqueue/coalesce a
			// frame; nothing here owns a per-run cancel registration.
			// result.NodeID is zero when Ran=false.
			if result.Ran && result.NodeID != (shared.UUID{}) {
				activeMu.Lock()
				activeNodes[result.NodeID] = struct{}{}
				activeMu.Unlock()
				defer func() {
					activeMu.Lock()
					delete(activeNodes, result.NodeID)
					activeMu.Unlock()
				}()
			}

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
			if err := cfg.Storage.Supervisors().Unregister(context.Background(), cfg.SupervisorID, nil); err != nil {
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
