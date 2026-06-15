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
//     per-active-node heartbeats, refresh `rimsky_claim_handles` rows owned
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
//	Every DELETE FROM rimsky_claim_handles and every UPDATE
//	rimsky_node_runs SET claimed_by = NULL is gated on
//	`AND … = supervisor_id`. The §7.5 heartbeat refresh below is a
//	WRITE (not a release), but it inherits the same claimant guard:
//	the WHERE clause is `holder_supervisor_id = $1`, so a stale
//	heartbeat from a different supervisor can never extend a row it
//	doesn't own. Concrete enforcement of the release path lives
//	across persistence.Queue / persistence.ClaimHandleTable impls,
//	`runtime/runner.go`, and `graph/scheduler/scheduler.go`;
//	this file's contribution is the heartbeat-write claimant guard.
//	Do not relax the `holder_supervisor_id = $1` predicate on the
//	heartbeat UPDATE.
//
// @blessed-invariant 10: Lock acquisition is atomic with parent-run
// claim acquisition. Per spec §4.10 + the 2026-05-15 data-platform-
// extensions §Recursive scope partitioning:
//
//	The §7.3 acquisition transaction either claims the parent run
//	AND inserts the parent `rimsky_claim_handles` row AND inserts all
//	sub-claim handle rows for opted-into partitioning AND records the
//	`ClaimProducer.Open`-returned addresses, or none of these. The
//	producer's own state mutations run in a producer-internal
//	transaction decoupled from rimsky's. Single-writer-per-scope
//	(invariant 4b) holds because rimsky's conflict predicate gates
//	lock-holder INSERTs against `rimsky_claim_handles` only. The
//	acquisition tx lives in `runtime/runner_acquire.go`; this file's
//	contribution is structural — the heartbeat refresh below MUST
//	NOT extend rows for nodes that have transitioned out of
//	`running`. The `holder_node_id IN (running-nodes)` filter keeps
//	the orphan-reap cutoff (5 × heartbeat_interval) attainable so
//	stranded held subgraphs are eventually reaped.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin"
	loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
)

// Config is the supervisor's construction-time dependency bundle.
type Config struct {
	SupervisorID string
	// Persist is the persistence.Tables handle (rimsky_* tables). Required.
	Persist persistence.Tables
	// Queue is the dispatch-queue accessor. Required.
	Queue persistence.Queue
	// AdvisoryLocker carries advisory-lock primitives the acquisition tx uses.
	// Required.
	AdvisoryLocker persistence.AdvisoryLocker
	Clock          shared.Clock
	Logger         shared.Logger
	Concurrency    int
	// HeartbeatInterval defaults to 5s when zero.
	HeartbeatInterval time.Duration
	// ClaimPollInterval defaults to 1s when zero.
	ClaimPollInterval time.Duration
	Resolver          executor.Resolver
	// StoreRegistry is the per-process store registry built by
	// config.StartSupervisor from the YAML stores config (spec §6.1).
	// The supervisor uses it for: deriving `accepted_stores` at
	// registration (§6.2), and looking up concrete *locks.ClaimProducer values
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
	// consecutive retries with no settling_signal_type change. Threaded
	// into both the synchronous RunArgs (built per tryClaim) and the
	// async-callback CallbackServer so async-callback-driven retries
	// observe the same cap.
	MaxRetriesWithoutProgressDefault int
	// ExpectedAttributesSchemaFor returns the named executor's advertised
	// expected_attributes_schema bytes. Threaded into RunArgs so the
	// dispatch path can compute the per-node effective attribute schema
	// (executor schema ∪ L1 defaults ∪ L2 node declaration) at
	// dispatch. Nil → executor contributes no schema to the merge (used
	// in unit tests).
	//
	// @concept: attribute
	ExpectedAttributesSchemaFor func(executorName string) (schema []byte, ok bool)
	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook (plan I1/I2/I3). Optional; nil → no-op everywhere.
	Metrics MetricsHook
	// MaxParkDuration is the deployment-level per-reason max_park_duration
	// cap map (keys are ParkReason storage-form strings; values are
	// caps). Threaded into the conductor's SweepParkedNodes path so the
	// watchdog can fail parked runs that overrun their per-reason cap
	// even when the per-row col:rimsky_node_runs.max_park_duration_seconds
	// is NULL. Per spec
	// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
	// §Parked-state taxonomy. Empty / nil → only per-row caps fire.
	MaxParkDuration map[string]time.Duration

	// LifecycleSubs is the supervisor's outbound LifecycleSubscriber
	// registry. Threaded into RunArgs / CallbackServer so the sub-graph
	// and fanout-partition RunScope close sites can fire
	// OnRunScopeTerminal. Nil → run-scope fan-out is a no-op (the lint /
	// unit-test path; production wiring populates it via
	// config.StartSupervisor → DialLifecycleSubscribers).
	//
	// Per spec 2026-05-24-host-agent-and-proxy-design.md.
	LifecycleSubs *locks.LifecycleRegistry
	// LifecyclePeersForSpec resolves the late-bind-aware peer set for a
	// template at run-scope close time. Function pointer populated at the
	// cmd/ entrypoint (control/ layer) so runtime/ never imports control/.
	// Nil → run-scope fan-out is a no-op.
	LifecyclePeersForSpec func(tplSpec node.TemplateSpec) []string
	// LateBindServiceProxies maps protocol name → proxy service name
	// (rimsky.yml late_bind_service_proxies). Threaded into RunArgs so the
	// §7.3 SelectCandidates admit-list extension can offer the proxy peer
	// as a stand-in for late-bound executor / claim-producer references.
	// Empty → the admit-list extension is inert.
	LateBindServiceProxies map[string]string

	// DataProcessors resolves a producer name to its DataProcessing client
	// (the `data_processing` mix-in protocol). Threaded into RunArgs so the
	// fan-out acquisition path can mint a candidate per sub-claim
	// (`BeginCandidate`) and the terminal path can Commit/Abandon those
	// candidates. Nil → the supervisor treats every producer as
	// non-DataProcessing (no candidate handles minted; the leaf falls back
	// to claim_scope_data + parent address). Populated by
	// `config.StartSupervisor` via `DialPublisherAndValidationRegistries`.
	//
	// @concept: data-processing
	DataProcessors DataProcessingRegistry

	// ExtraInprocHandlers lets a test or embedder register additional
	// inproc executor handlers alongside the rimsky-bundled builtins.
	// Keys are the inproc URL (e.g. `inproc://test-scratch-writer`); the
	// supervisor calls InProcessRegistry.Register for each entry after
	// builtin.RegisterAll so duplicate URLs surface as a startup error.
	// The supervisor also seeds the resolver with the key as both the
	// executor alias AND the URL — tests reference the handler by that
	// alias in their template's `executor:` field. Empty / nil → the
	// supervisor wires only the rimsky-bundled builtins (production
	// shape). Per TD-inproc-registry: "the registry is constructible in
	// tests with arbitrary handler sets". @concept: executor
	ExtraInprocHandlers map[string]executor.InProcessHandler
}

// Handle is returned by Start. Callers drive lifecycle via Shutdown and
// inspect the callback endpoint via CallbackAddr.
type Handle struct {
	stop          chan struct{}
	done          chan struct{}
	addr          string
	advertisedURL string
	// callbackReg is the supervisor's CallbackRegistry. Exposed via
	// CallbackRegistry() for test-only callers that need to register
	// auxiliary ack_ids against an in-flight run (e.g. the F4 scenario
	// pinning the callback-determinism rule).
	callbackReg *CallbackRegistry
	// @constraint: callback server's serve-loop fail channel (see
	// CallbackServer.ServeErr); exposed via CallbackServeErr so the
	// launch wiring can forward a post-start listener death onto the
	// role fail channel.
	callbackServeErr <-chan error
	// wg tracks in-flight run goroutines spawned by tryClaim so Shutdown can
	// wait on their completion without a polling loop.
	wg sync.WaitGroup
}

// Shutdown signals the main loop to stop claiming, waits for active runs to
// drain, closes the callback server, and unregisters the supervisor.
func (h *Handle) Shutdown(ctx context.Context) error {
	select {
	case <-h.stop:
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

// CallbackRegistry returns the supervisor's CallbackRegistry. Exposed for
// test-only callers that need to register auxiliary ack_ids against an
// in-flight run — e.g. the F4 callback-determinism scenario at
// test/scenarios/fanout_callback_determinism_e2e_test.go pinning the
// rejected_run_terminal ack_status branch of
// runtime/callback.go::driveTerminal. Production callers should NOT
// reach into the registry directly; the supervisor's own runner_dispatch
// path registers the per-dispatch ack_id at the AwaitAsyncCallback
// terminal.
func (h *Handle) CallbackRegistry() *CallbackRegistry { return h.callbackReg }

// CallbackServeErr surfaces a fatal post-start death of the supervisor's
// async-callback HTTP serve loop. At most one error is ever sent; the
// channel closes when the serve loop exits (a clean Shutdown closes it
// with no error sent). Supervising callers (the launch role runner)
// forward it onto the role fail channel so a supervisor with a dead
// callback listener restarts instead of running degraded.
func (h *Handle) CallbackServeErr() <-chan error { return h.callbackServeErr }

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
	// @constraint: the unified-invalidate adapter must be shared between
	// the sync path (per-tryClaim RunArgs below) and the async-callback
	// path (CallbackServer). Without this, handler-emitted invalidates
	// that arrive via async callback fall back to bare InvalidateNode
	// and cannot wake parked targets through the H2 unified path.
	//
	// @constraint: Metrics threaded through so InvalidateNode's
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
	// @constraint: shadow ExpectedAttributesSchemaFor so the inproc
	// executor's advertised schema is visible to BOTH the dispatch-time
	// effective-attribute-schema merge (RunArgs) AND the async-callback
	// path (CallbackServer). Wrap before callbackSrv construction so
	// both consumers see the same hook. Wrapping (rather than
	// replacing) keeps the operator-configured hook live for every
	// non-inproc executor.
	baseSchemaHook := cfg.ExpectedAttributesSchemaFor
	cfg.ExpectedAttributesSchemaFor = func(name string) ([]byte, bool) {
		if name == loop_counter.ExecutorAlias {
			return loop_counter.SchemaBytes(), true
		}
		if baseSchemaHook != nil {
			return baseSchemaHook(name)
		}
		return nil, false
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
		ExpectedAttributesSchemaFor:      cfg.ExpectedAttributesSchemaFor,
		Metrics:                          cfg.Metrics,
		LifecycleSubs:                    cfg.LifecycleSubs,
		LifecyclePeersForSpec:            cfg.LifecyclePeersForSpec,
		DataProcessors:                   cfg.DataProcessors,
	}
	addr, err := callbackSrv.Start(cfg.CallbackHost, cfg.CallbackPort)
	if err != nil {
		return nil, err
	}

	// @deliberate: inproc registry + kind-alias map. Every utility
	// handler in scope ships under lib/runtime/executor/builtin/<name>/.
	// The single registration site is `builtin.RegisterAll` — both this
	// supervisor path and the control-API's buildKindAliases route
	// through that shared helper, so a new utility executor added to the
	// builtin package surfaces in both processes by editing one file
	// (lib/runtime/executor/builtin/builtins.go) rather than two. Built
	// up before NewClientPoolWithInProcess so the inproc transport case
	// in ClientPool.GetOrCreate has a non-nil registry to consult.
	//
	// @constraint: seed the inproc handler registry AND the resolver
	// FIRST so the accepted_executors snapshot captured below (the row
	// the §7.3 candidate selector filters dispatches against) includes
	// every builtin inproc alias. Without that, dispatches keyed on
	// e.g. `rimsky.loop_counter` would never surface as candidates for
	// THIS supervisor, silently breaking STORY-inproc-utility-executor's
	// "no operator config needed" property.
	//
	// @concept: executor
	// @concept: node
	inprocReg := executor.NewInProcessRegistry()
	kindAliases := node.NewKindAliasMap()
	if err := builtin.RegisterAll(inprocReg, kindAliases); err != nil {
		_ = callbackSrv.Close(context.Background())
		return nil, fmt.Errorf("supervisor: %w", err)
	}
	// @deliberate: dispatch-side does not consult kind aliases — they're
	// a template-registration surface; constructed alongside the registry
	// only so the shared helper stays the single registration site.
	_ = kindAliases

	// @deliberate: extra inproc handlers (tests / embedders) register
	// after the builtins so a duplicate URL surfaces deterministically.
	// The same URL is used as the resolver alias — tests reference the
	// handler via `executor: <url>` in their templates. The duplicate-URL
	// guard inside InProcessRegistry.Register is the deterministic error
	// surface; a duplicate alias overwrite at the resolver layer would
	// silently shadow a builtin, which is the failure mode this guard
	// protects against.
	//
	// @concept: executor
	for url, h := range cfg.ExtraInprocHandlers {
		if err := inprocReg.Register(url, h); err != nil {
			_ = callbackSrv.Close(context.Background())
			return nil, fmt.Errorf("supervisor: register extra inproc handler %q: %w", url, err)
		}
		if !seedInprocExecutorAlias(cfg.Resolver, url, executor.Endpoint{Transport: "inproc", URL: url}) {
			cfg.Logger.Warn("supervisor: inproc executor alias seed skipped: resolver shape unrecognised",
				"alias", url,
				"resolver_type", fmt.Sprintf("%T", cfg.Resolver))
		}
	}

	// @deliberate: seed the resolver with inproc executor aliases so the
	// dispatch path's Resolver.Resolve(alias) returns the inproc endpoint
	// without operator config. Production resolvers are *StaticResolver
	// directly, or a *LateBindResolver wrapping a *StaticResolver in
	// deployments that configure LateBindServiceProxies. Walk through
	// the LateBindResolver wrapper to reach the inner static map —
	// without this, late-bind deployments fail to seed the inproc alias
	// and `kind: loop_counter` resolves to unresolved_executor, silently
	// breaking STORY-inproc-utility-executor's "no operator config
	// needed" property in late-bind deployments. Test fakes that wrap a
	// different shape silently skip seeding — tests that need inproc
	// dispatch wire their own resolver entry.
	for alias, endpoint := range builtin.BuiltinExecutorAliases() {
		if !seedInprocExecutorAlias(cfg.Resolver, alias, endpoint) {
			cfg.Logger.Warn("supervisor: builtin inproc executor alias seed skipped: resolver shape unrecognised",
				"alias", alias,
				"resolver_type", fmt.Sprintf("%T", cfg.Resolver))
		}
	}

	// @constraint: register the *advertised* callback host:port — the
	// address peers use to reach this supervisor — into
	// rimsky_supervisors, NOT the listener bind address (e.g. 0.0.0.0),
	// which is not dialable. Falls back to the listener host:port when
	// no advertise host is configured. AcceptedNames is read AFTER the
	// inproc seeding so the supervisor row carries every builtin (and
	// test-extra) inproc alias on its accepted_executors list — without
	// that, the §7.3 candidate selector filters out every dispatch row
	// whose executor_name resolves to an inproc alias.
	host, port := effectiveCallbackHostPort(addr, cfg.CallbackAdvertiseHost, cfg.CallbackAdvertisePort)
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

	// @deliberate: the factory matches the typed-UUID
	// HandlerContextFactory signature — the InProcessClient parses
	// req.DispatchId and req.NodeId into typed UUIDs at its Execute
	// entry point and threads them in here. Each closure call binds a
	// fresh ScratchWriter wired to the current dispatch's row.
	persistCap := cfg.Persist
	queueCap := cfg.Queue
	blobCap := cfg.Blob
	spillCap := cfg.BlobSpillThreshold
	loggerCap := cfg.Logger
	newHctx := executor.HandlerContextFactory(func(dispatchID, nodeID shared.UUID) executor.HandlerContext {
		return executor.HandlerContext{
			Scratch: &executor.ScratchWriter{
				Persist:        persistCap,
				Queue:          queueCap,
				Blob:           blobCap,
				SpillThreshold: spillCap,
				DispatchID:     dispatchID,
				NodeID:         nodeID,
				Logger:         loggerCap,
			},
		}
	})
	clientPool := executor.NewClientPoolWithInProcess(inprocReg, newHctx)
	advertised := advertisedCallbackURL(addr, cfg.CallbackAdvertiseHost, cfg.CallbackAdvertisePort)
	h := &Handle{stop: make(chan struct{}), done: make(chan struct{}), addr: addr, advertisedURL: advertised, callbackReg: callbackReg, callbackServeErr: callbackSrv.ServeErr()}
	go runLoop(cfg, h, callbackSrv, callbackReg, clientPool, accepted, acceptedStores, lockHolders)
	return h, nil
}

// seedInprocExecutorAlias walks the resolver chain to find the inner
// *StaticResolver and registers (alias, endpoint) on it. Handles both
// the production-shape configurations: a direct *StaticResolver, and
// a *LateBindResolver wrapping a *StaticResolver (set when the
// deployment configures LateBindServiceProxies). Without the
// LateBindResolver unwrap, late-bind deployments would silently fail
// the type assertion and the inproc alias would never land — breaking
// STORY-inproc-utility-executor's "no operator config needed" property
// in the production-shape configuration.
//
// Returns true when the alias was successfully seeded onto a
// *StaticResolver and false when the resolver chain ended on a shape
// the walker doesn't recognise (e.g. a third-party wrapping resolver
// that adds observability or rate-limiting). The caller logs the
// silent-skip case so an unrecognised resolver shape doesn't quietly
// break inproc dispatch — `kind: loop_counter` would otherwise resolve
// to `unresolved_executor` with no diagnostic.
//
// @concept: executor
func seedInprocExecutorAlias(r executor.Resolver, alias string, ep executor.Endpoint) bool {
	for {
		switch s := r.(type) {
		case *executor.StaticResolver:
			s.Register(alias, ep)
			return true
		case *executor.LateBindResolver:
			r = s.Unwrap()
			if r == nil {
				return false
			}
		default:
			return false
		}
	}
}

// storeRegistryNames returns a sorted-stable copy of the registry's
// producer names. Used at registration time to populate
// `accepted_stores` per spec §6.2 (operator-declared producer set the
// supervisor pool can satisfy).
func storeRegistryNames(reg *locks.Registry) []string {
	producers := reg.Producers()
	if len(producers) == 0 {
		return nil
	}
	out := make([]string, 0, len(producers))
	for name := range producers {
		out = append(out, name)
	}
	return out
}

// effectiveCallbackHostPort resolves the host:port that peers use to reach
// this supervisor for async-handoff callbacks. Preference order:
// (advertiseHost, advertisePort) → (advertiseHost, listener port) →
// listener host:port. The listener bind host (e.g. 0.0.0.0 in a container)
// is the last resort: operators set advertiseHost when the bind address is
// not reachable by executors. This single value is both handed to executors
// as `callback_url` and persisted to rimsky_supervisors, so a peer reading
// the row always gets a dialable address.
func effectiveCallbackHostPort(listenerAddr, advertiseHost string, advertisePort int) (string, int) {
	bindHost, bindPort := splitHostPort(listenerAddr)
	if advertiseHost == "" {
		return bindHost, bindPort
	}
	if advertisePort == 0 {
		return advertiseHost, bindPort
	}
	return advertiseHost, advertisePort
}

// advertisedCallbackURL computes the base URL the supervisor hands to
// executors as `callback_url`, built from effectiveCallbackHostPort.
func advertisedCallbackURL(listenerAddr, advertiseHost string, advertisePort int) string {
	host, port := effectiveCallbackHostPort(listenerAddr, advertiseHost, advertisePort)
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
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
	lockHolders persistence.ClaimHandleTable,
) {
	defer close(h.done)
	cfg.Logger.Info("supervisor started",
		"supervisor_id", cfg.SupervisorID,
		"accepts", accepted,
		"concurrency", cfg.Concurrency)

	// @constraint: activeCount tracks the supervisor's in-flight
	// tryClaim goroutines (incremented before launch, decremented when
	// the goroutine exits). Used for the concurrency guard below and
	// the shutdown drain wait; NOT used for the supervisor's
	// `active_node_count` heartbeat field — that comes from a DB query
	// so async dispatches whose goroutines have already returned
	// (post-AwaitAsyncCallback) are still counted. See doHeartbeat
	// below.
	var activeMu sync.Mutex
	activeCount := 0

	heartbeatTick := time.NewTicker(cfg.HeartbeatInterval)
	defer heartbeatTick.Stop()
	claimTick := time.NewTicker(cfg.ClaimPollInterval)
	defer claimTick.Stop()

	doHeartbeat := func() {
		hbCtx := context.Background()

		// @constraint: DB-driven view of "what this supervisor is
		// currently running." The result of this query drives both the
		// per-node last_heartbeat_at refresh below AND the
		// active_node_count we stamp into rimsky_supervisors via
		// Supervisors().Heartbeat. Both must reflect the same source of
		// truth — the in-memory goroutine counter (`activeCount`,
		// retained for the concurrency guard below) under-counts async
		// dispatches whose RunNode goroutine returned at
		// AwaitAsyncCallback while the actual work continues on the
		// executor side. Reading the DB here covers sync + async + any
		// future "RunNode returned early" path.
		var running []persistence.NodeRow
		if err := cfg.Persist.Transaction(hbCtx, func(ctx context.Context, tx persistence.Tx) error {
			rows, err := cfg.Persist.Nodes().ListRunningBySupervisor(ctx, cfg.SupervisorID, tx)
			running = rows
			return err
		}); err != nil {
			cfg.Logger.Warn("supervisor: list running nodes by supervisor failed", "error", err.Error())
			// @deliberate: fall through to the supervisor + lock-holder
			// heartbeats with a zero count — keeping THIS supervisor's
			// row alive matters more than the running-nodes count being
			// momentarily wrong.
			running = nil
		}

		if err := cfg.Persist.Transaction(hbCtx, func(ctx context.Context, tx persistence.Tx) error {
			return cfg.Persist.Supervisors().Heartbeat(ctx, cfg.SupervisorID, len(running), tx)
		}); err != nil {
			cfg.Logger.Warn("supervisor: supervisors.Heartbeat failed", "error", err.Error())
		}
		// @constraint: §7.5 lock-holder heartbeat refresh.
		// ExtendHeartbeat issues the SQL with the running-nodes
		// subquery filter so preserve-for-resume rows (anchored to
		// nodes that have transitioned out of `running` to `stale`) are
		// NOT refreshed — the resume-grace cutoff would never fire
		// otherwise. The expiresAt budget is `5 × HeartbeatInterval`
		// per the spec; the persistence layer converts the duration
		// back to integer seconds for the §7.5 SQL literal.
		expiresAt := cfg.Clock.Now().Add(5 * cfg.HeartbeatInterval)
		if err := cfg.Persist.Transaction(hbCtx, func(ctx context.Context, tx persistence.Tx) error {
			return lockHolders.ExtendHeartbeat(ctx, cfg.SupervisorID, expiresAt, tx)
		}); err != nil {
			cfg.Logger.Warn("supervisor: lockHolders.ExtendHeartbeat failed", "error", err.Error())
		}
		now := cfg.Clock.Now()
		for _, n := range running {
			nodeID := n.ID
			if n.RunScopeID == nil {
				continue
			}
			runScopeID := *n.RunScopeID
			if err := cfg.Persist.Transaction(hbCtx, func(ctx context.Context, tx persistence.Tx) error {
				return cfg.Persist.Nodes().UpdateHeartbeat(ctx, nodeID, runScopeID, now, cfg.SupervisorID, tx)
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
		// @deliberate: reserve a slot before launching. RunNode does its
		// own §7.3 candidate selection; if there's no eligible
		// candidate it bails quickly and we release the slot in the
		// defer below.
		activeCount++
		activeMu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer cancel()
			// @constraint: build the unified-invalidate adapter on the
			// supervisor's own persistence handle so handler-emitted
			// invalidates (H2 on_event) wake parked targets through the
			// same path as the admin endpoint (G3) and the parked-node
			// sweep (E3). Metrics fallback matches the async-callback
			// adapter above.
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
				ExpectedAttributesSchemaFor:      cfg.ExpectedAttributesSchemaFor,
				Metrics:                          cfg.Metrics,
				LifecycleSubs:                    cfg.LifecycleSubs,
				LifecyclePeersForSpec:            cfg.LifecyclePeersForSpec,
				LateBindServiceProxies:           cfg.LateBindServiceProxies,
				DataProcessors:                   cfg.DataProcessors,
			}, reg.Register)
			if runErr != nil {
				cfg.Logger.Warn("supervisor: RunNode failed", "error", runErr.Error())
			}

			// @deliberate: per-node heartbeat tracking is DB-driven (see
			// doHeartbeat above) — no in-memory bookkeeping of
			// result.NodeID is needed here. The heartbeat tick reads
			// `rimsky_nodes WHERE state='running' AND assigned_supervisor_id=$self`
			// directly, which is correct for both sync (RunNode
			// in-flight) and async (handed off, still running in the
			// DB) dispatches.

			defer func() {
				activeMu.Lock()
				activeCount--
				activeMu.Unlock()
			}()

			// @constraint: async path — the callback endpoint owns
			// dispatch cleanup.
			if result.Async {
				return
			}
			// @deliberate: defensive idempotent re-completion.
			// Post-2026-05-21 lifecycle reorder, every apply* terminal
			// function flips the dispatch row to a terminal phase
			// inside its own tx (applyTerminalComplete via
			// RemoveForNodeInTx; applyErrorPolicy +
			// applyTerminalInfraError the same; applyTerminalPark via
			// ParkActiveInTx). This outer call is a
			// WHERE-clause-guarded no-op on every known happy path; it
			// survives as a belt-and-suspenders against any future
			// terminal path that forgets to flip in-tx.
			if result.Ran && result.DispatchID != (shared.UUID{}) {
				_ = cfg.Queue.Complete(context.Background(), result.DispatchID, cfg.SupervisorID)
			}
		}()
	}

	for {
		select {
		case <-h.stop:
			// @deliberate: shutdown — stop claiming and wait up to 30s
			// for active runs to drain via wg. Using a WaitGroup
			// (incremented inside tryClaim when a run goroutine
			// launches, decremented on its defer) replaces the old 50ms
			// polling loop.
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
