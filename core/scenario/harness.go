// Package scenario provides a full-stack test harness spinning up every
// in-process component (scheduler, supervisor, stub executor, control API)
// against a testcontainers Postgres. Used by test/scenarios/*_test.go.
//
// Per the stores redesign (spec §16.2 inventory), the harness wires the new
// store-based subsystem rather than the retired resource layer:
//
//   - control-api + supervisor are constructed with a *store.Registry
//     containing the two stub stores ("stub_filesystem" + "stub_claim_store");
//     scenario tests that need real claim-store-postgres / filesystem
//     behaviour can pass extra factories via HarnessOpts.
//   - the scheduler is wired with both the store registry and a
//     *store.LockHoldersClient so the §13.5 step-2 (lock-holder),
//     step-3 (claim-holder), and step-4 (visibility-timeout) sweeps run.
//   - templateSpecToJSON emits the new node grammar (`stores`, `locks`,
//     `attributes`, `claim_resolutions`, `quality_rules`); concurrency-tag /
//     owns-resources / reads-resources / restore-version keys were retired
//     in spec §11.3.
//
// The harness preserves shared.Clock injection on every long-running
// component so scenario tests that need to advance time past the orphan-reap
// cutoff (5 × heartbeat_interval) can pass a *shared.ControllableClock via
// HarnessOpts.Clock and drive it from the test goroutine.
package scenario

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"io"
	"log/slog"

	"github.com/fallguy/rimsky/core/config"
	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	storagepkg "github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
	"github.com/fallguy/rimsky/core/supervisor"
	stubexec "github.com/fallguy/rimsky/executors/stub"
)

// Harness bundles every in-process component wired against a single
// testcontainers Postgres instance. All fields are safe to access from the
// test goroutine; background goroutines (scheduler, supervisor loop, HTTP
// servers) are torn down via t.Cleanup hooks registered in Start.
type Harness struct {
	T          testing.TB
	Ctx        context.Context
	Pool       *pgxpool.Pool
	Storage    storagepkg.StorageBackend
	Queue      queue.DispatchQueue
	Stub       *stubexec.Stub
	StubAddr   string
	Scheduler  config.SchedulerHandle
	Supervisor *supervisor.Handle
	ControlAPI config.ControlAPIHandle
	// ControlBase is the base URL of the in-process control-api
	// (http://host:port). DeployTemplate / CreateInstance POST against this.
	ControlBase string
	// Clock is the shared.Clock injected into every long-running component.
	// Defaults to shared.SystemClock{}; scenarios that need deterministic
	// time advancement pass HarnessOpts.Clock = shared.NewControllableClock(...)
	// and drive it from the test goroutine.
	Clock shared.Clock
	// Stores is the per-harness *store.Registry shared between supervisor +
	// control-api + scheduler. Pre-built with `stub_filesystem` and
	// `stub_claim_store` factories registered, plus any factories supplied
	// via HarnessOpts.ExtraStoreFactories. The configured store names + cfg
	// come from HarnessOpts.StoresConfig.
	Stores *store.Registry
}

// HarnessOpts tweaks which components the harness starts. Zero value yields
// scheduler + supervisor + stub executor + control-api wired in the default
// fast-tick configuration used by scenario tests, with the two stub-store
// factories registered and no built stores.
type HarnessOpts struct {
	// ExtraExecutors registers these executor endpoints in addition to the
	// stub. The stub binds at "stub" and "testexec" by default.
	ExtraExecutors map[string]executor.Endpoint

	// NoSupervisor skips starting the supervisor (for scenarios that drive
	// claims manually).
	NoSupervisor bool

	// NoScheduler skips starting the scheduler (for scenarios that want
	// manual tick control).
	NoScheduler bool

	// SchedulerTick overrides the default 250ms tick interval. Pass a long
	// interval (e.g. 1h) when driving ticks manually via Clock advancement.
	SchedulerTick time.Duration

	// HeartbeatInterval overrides the supervisor's heartbeat tick (default
	// 500ms) and the scheduler's heartbeat-timeout cutoff (default 5s).
	// Tests that exercise the orphan-reap path need a small interval so the
	// 5×interval cutoff is reachable in test time, or they can pass a
	// ControllableClock and advance it.
	HeartbeatInterval time.Duration

	// HeartbeatTimeout overrides the scheduler's stale-heartbeat / orphan-
	// claim cutoff (default 5s). The orphan-claim cutoff is 5× this value.
	HeartbeatTimeout time.Duration

	// Clock injects a shared.Clock into every long-running component
	// (scheduler, supervisor, control-api, callback server). Defaults to
	// shared.SystemClock{}. Pass a *shared.ControllableClock to drive time
	// deterministically.
	Clock shared.Clock

	// ExtraStoreFactories registers store factories in addition to the two
	// stub factories the harness registers by default. Use this to attach
	// real claim-store-postgres / filesystem factories for scenarios that
	// need them.
	ExtraStoreFactories []store.Factory

	// StoresConfig is the parsed YAML stores config (spec §14.1) the
	// registry builds at startup. Defaults to an empty map (no stores
	// built). Test helpers like withStores reference these store names from
	// the template grammar.
	StoresConfig store.StoresConfig
}

// Start spins up a full-stack harness against a fresh Postgres container.
// Cleanups are registered with t so callers typically don't need to do
// anything at test teardown.
func Start(t testing.TB, opts HarnessOpts) *Harness {
	t.Helper()
	ctx := context.Background()

	// pgtest requires *testing.T; scenario tests always pass one.
	tT, ok := t.(*testing.T)
	if !ok {
		t.Fatalf("scenario: Start requires *testing.T, got %T", t)
	}
	pool, teardownPg := pgtest.StartPostgres(ctx, tT)
	t.Cleanup(teardownPg)

	sb := pgstorage.New(pool)
	q := pgqueue.New(pool)

	clock := opts.Clock
	if clock == nil {
		clock = shared.SystemClock{}
	}

	heartbeatInterval := opts.HeartbeatInterval
	if heartbeatInterval == 0 {
		heartbeatInterval = 500 * time.Millisecond
	}
	heartbeatTimeout := opts.HeartbeatTimeout
	if heartbeatTimeout == 0 {
		heartbeatTimeout = 5 * time.Second
	}
	schedulerTick := opts.SchedulerTick
	if schedulerTick == 0 {
		schedulerTick = 250 * time.Millisecond
	}

	// Stub executor.
	s := stubexec.New()
	_, stubAddr := s.Listen(t)

	executors := map[string]executor.Endpoint{
		"stub":     {Transport: "grpc", URL: stubAddr},
		"testexec": {Transport: "grpc", URL: stubAddr}, // alias used by some scenarios
	}
	for k, v := range opts.ExtraExecutors {
		executors[k] = v
	}
	resolver := executor.NewStaticResolver(executors)

	// Per-harness store registry so parallel scenario tests don't alias each
	// other's in-memory stub state. Both stub factories are always registered;
	// callers add more via opts.ExtraStoreFactories.
	storeFactories := []store.Factory{
		stub.FilesystemFactory(),
		stub.ClaimStoreFactory(),
	}
	storeFactories = append(storeFactories, opts.ExtraStoreFactories...)

	h := &Harness{
		T:        t,
		Ctx:      ctx,
		Pool:     pool,
		Storage:  sb,
		Queue:    q,
		Stub:     s,
		StubAddr: stubAddr,
		Clock:    clock,
	}

	// Scheduler — wired with LockHolders + StoreRegistry so the §13.5
	// step-2 (lock-holder), step-3 (claim-holder), and step-4 (visibility-
	// timeout) sweeps run inside the harness.
	if !opts.NoScheduler {
		sh, err := config.StartScheduler(config.SchedulerConfig{
			Storage:              sb,
			Queue:                q,
			Clock:                clock,
			Logger:               shared.SilentLogger{},
			TickInterval:         schedulerTick,
			HeartbeatTimeout:     heartbeatTimeout,
			OrphanedClaimTimeout: 5 * heartbeatTimeout,
			Pool:                 pool,
			StoreFactories:       storeFactories,
			Stores:               opts.StoresConfig,
		})
		if err != nil {
			t.Fatalf("scenario: start scheduler: %v", err)
		}
		t.Cleanup(func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = sh.Shutdown(sctx)
		})
		h.Scheduler = sh
	}

	// Supervisor — wired with the same store factories + cfg so its
	// per-process registry matches the control-api's. The supervisor's
	// AcceptedStores is derived from the registry's built-store names; an
	// empty StoresConfig yields no built stores and the supervisor accepts
	// only nodes whose RequiredStores is empty.
	if !opts.NoSupervisor {
		sv, err := config.StartSupervisor(config.SupervisorConfig{
			SupervisorID:      "scenario-supervisor",
			Storage:           sb,
			Queue:             q,
			Clock:             clock,
			Logger:            shared.SilentLogger{},
			Concurrency:       4,
			HeartbeatInterval: heartbeatInterval,
			ClaimPollInterval: 100 * time.Millisecond,
			Resolver:          resolver,
			StoreFactories:    storeFactories,
			Stores:            opts.StoresConfig,
			CallbackHost:      "127.0.0.1",
			CallbackPort:      0,
		})
		if err != nil {
			t.Fatalf("scenario: start supervisor: %v", err)
		}
		t.Cleanup(func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = sv.Shutdown(sctx)
		})
		// The config wrapper returns a SupervisorHandle interface; the
		// scenario harness exposes the concrete *supervisor.Handle for
		// scenarios that need callback-addr inspection. Type-assert.
		if hh, ok := sv.(*supervisor.Handle); ok {
			h.Supervisor = hh
		}
	}

	// Control API. The store registry is built once inside StartControlAPI
	// from the same factories+cfg; we re-build it locally so harness
	// callers can introspect via h.Stores. Building twice is cheap (the
	// stub factories are stateless and BuildAll is idempotent on input);
	// the duplication is intentional to keep h.Stores observable without
	// reaching into the control-api's internals.
	regForHarness, err := buildStoreRegistry(storeFactories, opts.StoresConfig)
	if err != nil {
		t.Fatalf("scenario: build store registry: %v", err)
	}
	h.Stores = regForHarness

	ca, err := config.StartControlAPI(config.ControlAPIConfig{
		Storage:        sb,
		Queue:          q,
		Clock:          clock,
		Logger:         shared.SilentLogger{},
		Host:           "127.0.0.1",
		Port:           0,
		StoreFactories: storeFactories,
		Stores:         opts.StoresConfig,
	})
	if err != nil {
		t.Fatalf("scenario: start controlapi: %v", err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ca.Shutdown(sctx)
	})
	h.ControlAPI = ca
	h.ControlBase = "http://" + ca.Addr()

	return h
}

// buildStoreRegistry mirrors config.buildStoreRegistry (which is unexported).
// Kept tiny on purpose — the scenario harness uses it to surface the live
// registry for tests that want to seed claim items or inspect held regions.
func buildStoreRegistry(factories []store.Factory, cfg store.StoresConfig) (*store.Registry, error) {
	reg := store.NewRegistry()
	for _, f := range factories {
		reg.Register(f)
	}
	if len(cfg.Stores) == 0 {
		return reg, nil
	}
	if _, err := reg.BuildAll(cfg); err != nil {
		return nil, err
	}
	return reg, nil
}

// DeployTemplate marshals the spec to the control API's JSON schema and POSTs
// to /templates. Returns the new template_id or fails the test on any error.
//
// If spec.FrameResolution is empty (existing scenarios that pre-date the
// frame-resolution spec), the harness defaults to "serial_queue" so the
// scenarios run unchanged. Tests that exercise frame semantics explicitly
// should set FrameResolution themselves.
func (h *Harness) DeployTemplate(spec node.TemplateSpec) shared.UUID {
	h.T.Helper()
	if spec.FrameResolution == "" {
		spec.FrameResolution = node.FrameResolutionSerialQueue
	}
	body, err := json.Marshal(templateSpecToJSON(spec))
	if err != nil {
		h.T.Fatal(err)
	}
	resp, err := http.Post(h.ControlBase+"/templates", "application/json", bytesReader(body))
	if err != nil {
		h.T.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		h.T.Fatalf("DeployTemplate: status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		h.T.Fatalf("DeployTemplate: decode: %v", err)
	}
	id, err := parseUUIDStr(out.TemplateID)
	if err != nil {
		h.T.Fatalf("DeployTemplate: bad template_id %q: %v", out.TemplateID, err)
	}
	return id
}

// CreateInstance POSTs to /instances; returns instance_id.
func (h *Harness) CreateInstance(templateID shared.UUID, consumerKey string, params map[string]any) shared.UUID {
	h.T.Helper()
	body, err := json.Marshal(map[string]any{
		"template_id":  templateID.String(),
		"consumer_key": consumerKey,
		"params":       params,
	})
	if err != nil {
		h.T.Fatal(err)
	}
	resp, err := http.Post(h.ControlBase+"/instances", "application/json", bytesReader(body))
	if err != nil {
		h.T.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		h.T.Fatalf("CreateInstance: status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		h.T.Fatalf("CreateInstance: decode: %v", err)
	}
	id, err := parseUUIDStr(out.InstanceID)
	if err != nil {
		h.T.Fatalf("CreateInstance: bad instance_id %q: %v", out.InstanceID, err)
	}
	// Under frame resolution, the instance factory enqueues a frame for
	// root executor nodes; dispatch rows are created by the scheduler
	// tick (frame engine advances the frame, sweepReady enqueues the
	// dispatch). Tests that call RunNode synchronously after
	// CreateInstance need the dispatch row to exist; wait briefly for
	// the scheduler tick to materialize it. Skip the wait for instances
	// whose template has no root executor nodes (no dispatch will ever
	// be enqueued at instance-create time).
	h.waitForRootDispatch(id, 5*time.Second)
	return id
}

// waitForRootDispatch is a best-effort wait: it polls for any
// rimsky_dispatch row in the new instance up to timeout, returning when
// one exists or when timeout elapses. When the scheduler is not running
// (HarnessOpts.NoScheduler == true), no scheduler tick will advance the
// queued frame and create a dispatch row; this method drives a manual
// frame-advance + dispatch-enqueue path so NoScheduler tests still see
// the dispatch row materialize.
func (h *Harness) waitForRootDispatch(instanceID shared.UUID, timeout time.Duration) {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		err := h.Pool.QueryRow(h.Ctx, `
            SELECT count(*) FROM rimsky_dispatch d
            JOIN rimsky_nodes n ON n.id = d.node_id
            WHERE n.instance_id = $1
        `, instanceID).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		// No scheduler — drive frame engine + ready sweep manually.
		if h.Scheduler == nil {
			h.driveFrameAndEnqueue(instanceID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// driveFrameAndEnqueue manually advances any queued frame for the given
// instance to running and enqueues dispatch rows for newly-stale ready
// nodes. Used when NoScheduler is set so tests that synchronously hit
// RunNode after CreateInstance still find an eligible candidate.
//
// Best-effort: errors are silenced (the scheduler-running path swallows
// these too in production via the warn-and-continue pattern).
func (h *Harness) driveFrameAndEnqueue(instanceID shared.UUID) {
	h.T.Helper()
	// Advance the queued frame.
	_ = frame.RunTick(h.Ctx, h.Pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Enqueue dispatch rows for ready stale nodes in this instance.
	rows, err := h.Storage.Nodes().ListReadyForDispatch(h.Ctx, nil)
	if err != nil {
		return
	}
	for _, n := range rows {
		if n.InstanceID != instanceID || n.FrameID == nil {
			continue
		}
		_ = h.Queue.Enqueue(h.Ctx, queue.DispatchRequest{
			NodeID:         n.ID,
			ExecutorName:   n.Executor,
			RequiredStores: []string{},
			EnqueuedAt:     time.Now(),
			FrameID:        *n.FrameID,
		})
	}
}

// WaitForNodeState polls the node row until state matches or timeout
// elapses. Returns true when the state was observed.
//
// Under the frame model nodes start fresh (Create default), so a naive
// "wait for fresh" can short-circuit before any work runs. When the
// requested state is fresh, the helper additionally requires evidence
// of execution: either a work_completed or pure_cascade_commit event
// for this node. Tests that don't want this gating should call
// WaitForEventKind directly or use a non-fresh target state.
func (h *Harness) WaitForNodeState(nodeID shared.UUID, state shared.NodeState, timeout time.Duration) bool {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	requireRun := state == shared.NodeStateFresh
	for time.Now().Before(deadline) {
		n, err := h.Storage.Nodes().Get(h.Ctx, nodeID, nil)
		if err == nil && n != nil && n.State == state {
			if !requireRun || h.hasRunEvent(nodeID) {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// hasRunEvent reports whether at least one work_completed or
// pure_cascade_commit event has been recorded for nodeID.
func (h *Harness) hasRunEvent(nodeID shared.UUID) bool {
	var count int
	err := h.Pool.QueryRow(h.Ctx, `
        SELECT count(*) FROM rimsky_events
        WHERE node_id = $1 AND kind IN ('work_completed','pure_cascade_commit','no_op_commit')
    `, nodeID).Scan(&count)
	return err == nil && count > 0
}

// WaitForEventKind polls rimsky_events for any row with the given (node,
// kind) pair. Returns true when one is observed before timeout. Useful
// for tests that need to confirm a node ran without relying on terminal
// state (e.g. producer that returns to fresh whether changed=true or
// changed=false).
func (h *Harness) WaitForEventKind(nodeID shared.UUID, kind string, timeout time.Duration) bool {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		err := h.Pool.QueryRow(h.Ctx, `
            SELECT count(*) FROM rimsky_events
            WHERE node_id = $1 AND kind = $2
        `, nodeID, kind).Scan(&count)
		if err == nil && count > 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// WaitForDispatch polls until a rimsky_dispatch row exists for the given
// node, then returns. Tests that synchronously invoke RunNode after a
// CreateInstance need this to wait for the scheduler tick + frame engine
// to advance the initial frame and enqueue the dispatch row (under the
// frame-resolution model the dispatch enqueue is no longer instance-
// factory-time; it's scheduler-tick-time).
func (h *Harness) WaitForDispatch(nodeID shared.UUID, timeout time.Duration) bool {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_dispatch WHERE node_id = $1`, nodeID,
		).Scan(&count)
		if err == nil && count > 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// GetNodes fetches all nodes for an instance.
func (h *Harness) GetNodes(instanceID shared.UUID) []storagepkg.NodeRow {
	h.T.Helper()
	nodes, err := h.Storage.Nodes().ListByInstance(h.Ctx, instanceID, nil)
	if err != nil {
		h.T.Fatalf("GetNodes: %v", err)
	}
	return nodes
}

// FindNode returns the first node in the instance matching nodeType, or nil.
func (h *Harness) FindNode(instanceID shared.UUID, nodeType string) *storagepkg.NodeRow {
	for _, n := range h.GetNodes(instanceID) {
		if n.NodeType == nodeType {
			n := n
			return &n
		}
	}
	return nil
}

// templateSpecToJSON converts a node.TemplateSpec into the snake_case JSON
// shape expected by POST /templates. Mirrors controlapi.templateDeployRequest;
// the redesign retired concurrency_tags / owns_resources / reads_resources /
// restore_version (spec §11.3) — every node now declares its store usage,
// named locks, attribute schema, claim resolutions, and quality rules.
func templateSpecToJSON(spec node.TemplateSpec) map[string]any {
	nodes := make([]map[string]any, 0, len(spec.Nodes))
	for _, n := range spec.Nodes {
		nodes = append(nodes, templateNodeToJSON(n))
	}
	out := map[string]any{
		"name":             spec.Name,
		"version":          spec.Version,
		"frame_resolution": spec.FrameResolution,
		"nodes":            nodes,
	}
	if spec.FrameTimeoutMs > 0 {
		out["frame_timeout_ms"] = spec.FrameTimeoutMs
	}
	if spec.Description != "" {
		out["description"] = spec.Description
	}
	if len(spec.ParamsSchema) > 0 {
		out["params_schema"] = spec.ParamsSchema
	}
	if len(spec.ParamsRedact) > 0 {
		out["params_redact"] = spec.ParamsRedact
	}
	return out
}

// templateNodeToJSON encodes a single node def. Extracted so the per-node
// fan-out doesn't push the parent over the ~100-line cold-read function
// guideline.
func templateNodeToJSON(n node.TemplateNodeDef) map[string]any {
	nd := map[string]any{
		"type": n.Type,
	}
	if n.Description != "" {
		nd["description"] = n.Description
	}
	if n.Executor != "" {
		nd["executor"] = n.Executor
	}
	if len(n.Userdata) > 0 {
		nd["userdata"] = n.Userdata
	}
	if n.Schedule != "" {
		nd["schedule"] = n.Schedule
	}
	if len(n.Dependencies) > 0 {
		nd["dependencies"] = n.Dependencies
	}
	if len(n.Stores) > 0 {
		stores := make([]map[string]any, 0, len(n.Stores))
		for _, s := range n.Stores {
			stores = append(stores, storeRefToJSON(s))
		}
		nd["stores"] = stores
	}
	if len(n.Locks) > 0 {
		locks := make([]map[string]any, 0, len(n.Locks))
		for _, l := range n.Locks {
			lock := map[string]any{
				"name": l.Name,
				"mode": string(l.Mode),
			}
			if l.Limit != 0 {
				lock["limit"] = l.Limit
			}
			locks = append(locks, lock)
		}
		nd["locks"] = locks
	}
	if len(n.Attributes.Schema) > 0 {
		nd["attributes"] = map[string]any{"schema": n.Attributes.Schema}
	}
	if len(n.QualityRules) > 0 {
		qrs := make([]map[string]any, 0, len(n.QualityRules))
		for _, qr := range n.QualityRules {
			item := map[string]any{"type": qr.Type}
			if len(qr.Config) > 0 {
				item["config"] = qr.Config
			}
			if qr.Severity != "" {
				item["severity"] = string(qr.Severity)
			}
			qrs = append(qrs, item)
		}
		nd["quality_rules"] = qrs
	}
	if len(n.ClaimResolutions) > 0 {
		crs := make([]map[string]any, 0, len(n.ClaimResolutions))
		for _, cr := range n.ClaimResolutions {
			item := map[string]any{
				"source": cr.Source,
				"store":  cr.Store,
			}
			if cr.OnCommit != "" {
				item["on_commit"] = cr.OnCommit
			}
			if cr.OnGiveUp != "" {
				item["on_give_up"] = cr.OnGiveUp
			}
			crs = append(crs, item)
		}
		nd["claim_resolutions"] = crs
	}
	if len(n.ErrorTypes) > 0 {
		ets := map[string]any{}
		for cls, etp := range n.ErrorTypes {
			actions := make([]map[string]any, 0, len(etp.Policy))
			for _, a := range etp.Policy {
				act := map[string]any{"action": a.Action}
				if a.Count != 0 {
					act["count"] = a.Count
				}
				if a.Backoff != "" {
					act["backoff"] = string(a.Backoff)
				}
				if a.Jitter != "" {
					act["jitter"] = string(a.Jitter)
				}
				if a.BaseDelayMs != 0 {
					act["base_delay_ms"] = a.BaseDelayMs
				}
				if a.MaxDelayMs != 0 {
					act["max_delay_ms"] = a.MaxDelayMs
				}
				if len(a.Targets) > 0 {
					act["targets"] = a.Targets
				}
				if a.ReasonTemplate != "" {
					act["reason_template"] = a.ReasonTemplate
				}
				actions = append(actions, act)
			}
			ets[cls] = map[string]any{"policy": actions}
		}
		nd["error_types"] = ets
	}
	return nd
}

// storeRefToJSON encodes one node.NodeStoreRef per the JSON wire shape.
func storeRefToJSON(s node.NodeStoreRef) map[string]any {
	item := map[string]any{
		"name": s.Name,
	}
	if s.Claim {
		item["claim"] = true
	}
	if s.Hold {
		item["hold"] = true
	}
	if len(s.Write) > 0 {
		item["write"] = s.Write
	}
	if len(s.Read) > 0 {
		item["read"] = s.Read
	}
	if s.OnCommit != "" {
		item["on_commit"] = s.OnCommit
	}
	if s.OnGiveUp != "" {
		item["on_give_up"] = s.OnGiveUp
	}
	if s.Resumable {
		item["resumable"] = true
	}
	return item
}

// withStores returns a TemplateNodeDef option that appends store refs to the
// node. Fluent: scenario tests build a node spec by chaining option helpers.
//
// Usage:
//
//	node.TemplateNodeDef{Type: "worker", Executor: "stub"}
//	withStores(scenario.StoreRef("inbound", scenario.WithClaim(true)))(...)
//
// Kept lowercase so they're in-package helpers; scenario tests import the
// scenario package and call them via scenario.WithStores etc.
func withStores(refs ...node.NodeStoreRef) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Stores = append(n.Stores, refs...)
	}
}

// withLocks returns a TemplateNodeDef option that appends named-lock refs.
func withLocks(refs ...node.NodeLockRef) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Locks = append(n.Locks, refs...)
	}
}

// withAttributes returns a TemplateNodeDef option that sets the per-node
// attributes JSON Schema. Replaces a previous setting (the schema is a
// single map, not a list of fragments).
func withAttributes(schema map[string]any) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Attributes = node.NodeAttributesDef{Schema: schema}
	}
}

// withClaimResolutions returns a TemplateNodeDef option that appends one or
// more held-claim resolution refs.
func withClaimResolutions(refs ...node.ClaimResolutionRef) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.ClaimResolutions = append(n.ClaimResolutions, refs...)
	}
}

// MakeNode constructs a TemplateNodeDef by applying option helpers
// (withStores / withLocks / withAttributes / withClaimResolutions) to a
// base spec. Exported so scenario tests can call it.
//
// The base spec carries the always-required fields (Type, optionally
// Executor / Schedule / Dependencies / ErrorTypes / QualityRules); options
// fill in store / lock / attribute / claim-resolution wiring.
func MakeNode(base node.TemplateNodeDef, opts ...func(*node.TemplateNodeDef)) node.TemplateNodeDef {
	n := base
	for _, o := range opts {
		o(&n)
	}
	return n
}

// WithStores is the exported alias for withStores; scenario tests outside
// this package call it as scenario.WithStores(...).
func WithStores(refs ...node.NodeStoreRef) func(*node.TemplateNodeDef) {
	return withStores(refs...)
}

// WithLocks is the exported alias for withLocks.
func WithLocks(refs ...node.NodeLockRef) func(*node.TemplateNodeDef) {
	return withLocks(refs...)
}

// WithAttributes is the exported alias for withAttributes.
func WithAttributes(schema map[string]any) func(*node.TemplateNodeDef) {
	return withAttributes(schema)
}

// WithClaimResolutions is the exported alias for withClaimResolutions.
func WithClaimResolutions(refs ...node.ClaimResolutionRef) func(*node.TemplateNodeDef) {
	return withClaimResolutions(refs...)
}
