// Plan A Task 10.4 — supervisor main loop.
//
// Port of rimsky/src/supervisor/supervisor.ts. Spec §6.2 (per-dispatch flow),
// §17 (ownership @blessed-invariant). Supervisor:
//
//   - Registers self in rimsky_supervisors (callback host/port, accepted
//     executors, concurrency).
//   - Starts an HTTP callback server so async-handoff executors can POST
//     terminal outcomes (see callback.go).
//   - Loops:
//   - Heartbeat tick (HeartbeatInterval): update own supervisor row, update
//     per-active-node heartbeats, poll kill_requested and cancel matching
//     active runs.
//   - Claim tick (ClaimPollInterval): while active < concurrency, try to
//     claim one dispatch row via queue.Claim; on success, dispatch RunNode
//     in a goroutine.
//   - On shutdown: stop claiming, wait up to 30s for active runs, close the
//     callback server, unregister, close the executor client pool.
package supervisor

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
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
	GetResource       func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error)
	// ResourceFactories is the explicit factory registry consulted by the
	// supervisor during dispatch. Set by config.StartSupervisor; defaults
	// to resource.DefaultRegistry() when unset, so tests that construct
	// supervisor.Config directly still work.
	ResourceFactories *resource.FactoryRegistry
	CallbackHost      string
	CallbackPort      int
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

	callbackReg := NewCallbackRegistry()
	callbackSrv := &CallbackServer{
		Registry: callbackReg,
		Storage:  cfg.Storage,
		Queue:    cfg.Queue,
		Clock:    cfg.Clock,
		Logger:   cfg.Logger,
	}
	addr, err := callbackSrv.Start(cfg.CallbackHost, cfg.CallbackPort)
	if err != nil {
		return nil, err
	}

	// Parse host:port from addr for registration in rimsky_supervisors.
	host, port := splitHostPort(addr)
	accepted := cfg.Resolver.AcceptedNames()
	if err := cfg.Storage.Supervisors().Register(context.Background(), storage.SupervisorRegisterInput{
		ID:                cfg.SupervisorID,
		AcceptedExecutors: accepted,
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
	go runLoop(cfg, h, callbackSrv, callbackReg, clientPool, accepted)
	return h, nil
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
) {
	defer close(h.done)
	cfg.Logger.Info("supervisor started",
		"supervisor_id", cfg.SupervisorID,
		"accepts", accepted,
		"concurrency", cfg.Concurrency)

	var activeMu sync.Mutex
	activeRuns := map[shared.UUID]context.CancelFunc{}
	activeCount := 0

	heartbeatTick := time.NewTicker(cfg.HeartbeatInterval)
	defer heartbeatTick.Stop()
	claimTick := time.NewTicker(cfg.ClaimPollInterval)
	defer claimTick.Stop()

	doHeartbeat := func() {
		activeMu.Lock()
		cnt := activeCount
		ids := make([]shared.UUID, 0, len(activeRuns))
		for id := range activeRuns {
			ids = append(ids, id)
		}
		activeMu.Unlock()

		if err := cfg.Storage.Supervisors().Heartbeat(context.Background(), cfg.SupervisorID, cnt, nil); err != nil {
			cfg.Logger.Warn("supervisor: supervisors.Heartbeat failed", "error", err.Error())
		}
		for _, id := range ids {
			_ = cfg.Storage.Nodes().UpdateHeartbeat(context.Background(), id, cfg.Clock.Now(), cfg.SupervisorID, nil)
			// Poll kill_requested; cancel the run context if set. The runner's
			// stream will unwind and ApplyTerminalOutcome will classify the
			// cancellation as infra error or stream_error.
			n, _ := cfg.Storage.Nodes().Get(context.Background(), id, nil)
			if n != nil && n.KillRequested {
				activeMu.Lock()
				if cancel, ok := activeRuns[id]; ok {
					cancel()
				}
				activeMu.Unlock()
			}
		}
	}

	tryClaim := func() {
		activeMu.Lock()
		if activeCount >= cfg.Concurrency {
			activeMu.Unlock()
			return
		}
		activeMu.Unlock()

		// Empty limits = no per-tag caps. Per-tag cap wiring is a post-v1
		// concern (Plan B); supervisor currently does not consume
		// concurrency_limits config.
		row, err := cfg.Queue.Claim(context.Background(), cfg.SupervisorID, accepted, map[string]int{})
		if err != nil {
			cfg.Logger.Warn("supervisor: queue.Claim failed", "error", err.Error())
			return
		}
		if row == nil {
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		activeMu.Lock()
		activeRuns[row.NodeID] = cancel
		activeCount++
		activeMu.Unlock()

		h.wg.Add(1)
		go func(r *shared.DispatchRow) {
			defer h.wg.Done()
			defer func() {
				activeMu.Lock()
				delete(activeRuns, r.NodeID)
				activeCount--
				activeMu.Unlock()
				// cancel() frees the context associated with this run; the
				// inner runner already unwound. One cancel is sufficient —
				// the goroutine previously had a nested `defer cancel()` that
				// was redundant with this one.
				cancel()
			}()
			result, runErr := RunNode(ctx, RunArgs{
				Storage: cfg.Storage, Queue: cfg.Queue, Clock: cfg.Clock, Logger: cfg.Logger,
				NodeID: r.NodeID, DispatchID: r.ID,
				SupervisorID: cfg.SupervisorID,
				Pool:         pool, Resolver: cfg.Resolver,
				GetResource: cfg.GetResource,
				CallbackURL: h.advertisedURL,
			}, reg.Register)
			if runErr != nil {
				cfg.Logger.Warn("supervisor: RunNode failed",
					"node_id", r.NodeID.String(),
					"dispatch_id", r.ID.String(),
					"error", runErr.Error())
			}
			// Async path: the callback endpoint owns dispatch cleanup.
			if result.Async {
				return
			}
			// result.Ran covers both success and infra-error. For the infra
			// case ApplyTerminalOutcome has *already* enqueued a fresh
			// dispatch row (see terminal_outcome.go); the Commit below just
			// deletes the original row we claimed. For resolver-miss the node
			// transitioned directly to failed without enqueueing a retry, so
			// deleting the original row is also correct. Either way the
			// original claim row must be removed — runErr != nil does NOT
			// change that.
			if result.Ran {
				_ = cfg.Queue.Complete(context.Background(), r.ID, cfg.SupervisorID)
			}
			// result.Ran == false: claim was lost to another supervisor; we
			// must not touch the row.
		}(row)
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
