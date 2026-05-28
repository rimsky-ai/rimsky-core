// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package scenario provides a full-stack test harness spinning up every
// in-process component (scheduler, supervisor, stub executor, control
// API) against a testcontainers Postgres. Used by test/scenarios/.
//
// Per the v3 stores redesign the harness wires loopback gRPC store-
// service binaries (via stores/<kind>/testfixture.Start) — there are no
// in-process Factory instances anymore. Tests pass HarnessOpts.Stores
// (a config.RemoteStoresConfig) populated with endpoints, and the
// harness threads that through to the supervisor / scheduler /
// control-api startup.
package scenario

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"io"
	"log/slog"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	stubexec "github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	stubtest "github.com/rimsky-ai/rimsky-core/test/support/executors/stub/stubtest"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

// Harness bundles every in-process component wired against a single
// testcontainers Postgres instance.
type Harness struct {
	T   testing.TB
	Ctx context.Context
	// Pool is the underlying *pgxpool.Pool. Test-only escape hatch
	// (sourced via pgpersist.PoolFromDatabaseForTest) for scenario tests
	// that seed fixtures via raw SQL. Use Driver / Persist / Queue for
	// new code.
	Pool       *pgxpool.Pool
	Driver     persistence.Database
	Persist    persistence.Tables
	Queue      persistence.Queue
	Stub       *stubexec.Stub
	StubAddr   string
	Scheduler  config.SchedulerHandle
	Supervisor config.SupervisorHandle
	ControlAPI config.ControlAPIHandle
	// ControlBase is the base URL of the in-process control-api.
	ControlBase string
	// Clock is the shared.Clock injected into every long-running
	// component.
	Clock shared.Clock
}

// HarnessOpts tweaks which components the harness starts.
type HarnessOpts struct {
	// ExtraExecutors registers these executor endpoints in addition
	// to the stub.
	ExtraExecutors map[string]executor.Endpoint

	// NoSupervisor skips starting the supervisor.
	NoSupervisor bool

	// NoScheduler skips starting the scheduler.
	NoScheduler bool

	// SchedulerTick overrides the default 250ms tick interval.
	SchedulerTick time.Duration

	// HeartbeatInterval overrides the supervisor's heartbeat tick
	// (default 500ms) and the scheduler's heartbeat-timeout cutoff
	// (default 5s).
	HeartbeatInterval time.Duration

	// HeartbeatTimeout overrides the scheduler's stale-heartbeat /
	// orphan-claim cutoff (default 5s).
	HeartbeatTimeout time.Duration

	// Clock injects a shared.Clock into every long-running component.
	Clock shared.Clock

	// Stores is the operator-facing remote-stores config (per v3 spec
	// §6.1: name → endpoint + declared capabilities). Tests start
	// loopback store-services via stores/<kind>/testfixture.Start
	// before calling Start, then point endpoints at those addresses.
	Stores config.RemoteStoresConfig

	// NamedLocks is the operator-side named-lock config (per v3 spec
	// §6.1's `named_locks:` block). Without this, templates that
	// reference any named lock fail validation in scenario tests
	// because the always-on validator hook treats every name as
	// undeclared.
	NamedLocks locks.NamedLocksConfig

	// LateBindServiceProxies maps protocol name → proxy service name
	// (rimsky.yml late_bind_service_proxies). When non-empty the harness
	// wraps the executor resolver in a LateBindResolver (so late-bound
	// executor names route through the named proxy), threads the map into
	// the supervisor's SelectCandidates admit-list extension, and wires
	// the late-bind-aware LifecyclePeersForSpec on both supervisor and
	// control-api. Empty → the late-bind machinery is inert (today's
	// behavior). Per spec 2026-05-24-host-agent-and-proxy-design.md.
	LateBindServiceProxies map[string]string

	// ExecutorProtocols overrides the `protocols:` list for the named
	// executor entry (default: just "executor"). Used to mark the
	// host-agent-proxy as a lifecycle_subscriber so the control-api and
	// supervisor dial it for OnInstanceCreated / OnRunScopeTerminal
	// fan-out. Keys must also appear in ExtraExecutors.
	ExecutorProtocols map[string][]string
}

// Start spins up a full-stack harness against a fresh Postgres
// container.
func Start(t testing.TB, opts HarnessOpts) *Harness {
	t.Helper()
	ctx := context.Background()

	tT, ok := t.(*testing.T)
	if !ok {
		t.Fatalf("scenario: Start requires *testing.T, got %T", t)
	}
	driver := pgtest.OpenDriver(ctx, tT)
	pool, _ := pgpersist.PoolFromDatabaseForTest(driver)
	persistStore := driver.Tables()
	q := driver.Queue()

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

	s := stubexec.New()
	_, stubAddr := stubtest.Listen(t, s)

	executors := map[string]executor.Endpoint{
		"stub":     {Transport: "grpc", URL: stubAddr},
		"testexec": {Transport: "grpc", URL: stubAddr},
	}
	for k, v := range opts.ExtraExecutors {
		executors[k] = v
	}
	staticResolver := executor.NewStaticResolver(executors)
	var resolver executor.Resolver = staticResolver
	if len(opts.LateBindServiceProxies) > 0 {
		// Wrap the static resolver so late-bound executor names (absent
		// from the static map but present in an instance's service_bindings)
		// route through the configured proxy. The lookup reads the bindings
		// JSONB straight off the instance row.
		lookupBindings := func(lookupCtx context.Context, instanceID string) (map[string]json.RawMessage, bool, error) {
			id, parseErr := parseUUIDStr(instanceID)
			if parseErr != nil {
				return nil, false, parseErr
			}
			var row *persistence.InstanceRow
			if txErr := persistStore.Transaction(lookupCtx, func(ctx context.Context, tx persistence.Tx) error {
				r, err := persistStore.Instances().Get(ctx, id, tx)
				row = r
				return err
			}); txErr != nil {
				return nil, false, txErr
			}
			if row == nil || len(row.ServiceBindings) == 0 {
				return nil, false, nil
			}
			var bindings map[string]json.RawMessage
			if err := json.Unmarshal(row.ServiceBindings, &bindings); err != nil {
				return nil, false, err
			}
			return bindings, true, nil
		}
		resolver = executor.NewLateBindResolver(staticResolver, lookupBindings, opts.LateBindServiceProxies)
	}

	h := &Harness{
		T:        t,
		Ctx:      ctx,
		Pool:     pool,
		Driver:   driver,
		Persist:  persistStore,
		Queue:    q,
		Stub:     s,
		StubAddr: stubAddr,
		Clock:    clock,
	}

	if !opts.NoScheduler {
		sh, err := config.StartScheduler(config.SchedulerConfig{
			Driver:               driver,
			Clock:                clock,
			Logger:               shared.SilentLogger{},
			TickInterval:         schedulerTick,
			HeartbeatTimeout:     heartbeatTimeout,
			OrphanedClaimTimeout: 5 * heartbeatTimeout,
			Stores:               opts.Stores,
			NamedLocks:           opts.NamedLocks,
			// Required for SweepParkedNodes (E3) so parked-rows can be
			// resumed under the scheduler's own supervisor id.
			SupervisorID: "scenario-scheduler",
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

	// Scenario tests assume the stub executor (and any extras the test
	// declares) is reachable by dispatch time. The runtime fails dispatch
	// with `executor_schema_unavailable` when the resolver returns
	// ok=false for an executor referenced by a node — a deliberate hard
	// failure (see `runtime/runner_dispatch.go::resolveAttributes`). The
	// stub's real Capabilities response advertises no schema; this
	// resolver papers over that for the in-process harness by returning a
	// permissive object schema (no `properties` block ⇒
	// `node.IsPermissiveExecutorSchema` returns true ⇒ the readOnly-
	// fallback leg of the unified-attribute-surface check is skipped, the
	// way a permissive real executor is treated).
	expectedSchemaFor := func(executorName string) ([]byte, bool) {
		if _, known := executors[executorName]; !known {
			return nil, false
		}
		return []byte(`{"type":"object"}`), true
	}

	// Build the executors config (shared by supervisor + control-api). The
	// per-executor protocols list defaults to ["executor"]; ExecutorProtocols
	// overrides it (e.g. to mark the host-agent-proxy as a
	// lifecycle_subscriber so it's dialed for OnInstanceCreated /
	// OnRunScopeTerminal fan-out).
	executorsCfg := config.ExecutorsConfig{Executors: map[string]config.ExecutorEntry{}}
	for name, ep := range executors {
		protocols := []string{"executor"}
		if override, ok := opts.ExecutorProtocols[name]; ok {
			protocols = override
		}
		executorsCfg.Executors[name] = config.ExecutorEntry{
			Transport: ep.Transport,
			Endpoint:  ep.URL,
			TLS:       ep.TLS,
			Protocols: protocols,
		}
	}

	// Late-bind-aware lifecycle peer set: adds the proxy peer for templates
	// declaring late_bind_services. Shared by supervisor (OnRunScopeTerminal)
	// and control-api (OnInstanceCreated). Inert when no proxy is configured.
	var peersForSpec func(node.TemplateSpec) []string
	if len(opts.LateBindServiceProxies) > 0 {
		lateBindProxies := opts.LateBindServiceProxies
		peersForSpec = func(tplSpec node.TemplateSpec) []string {
			return controlapi.LifecyclePeersForSpec(
				controlapi.AppDeps{LateBindServiceProxies: lateBindProxies},
				tplSpec,
			)
		}
	}

	if !opts.NoSupervisor {
		// Diagnostic: surface supervisor logs when SCENARIO_DEBUG=1 is set
		// so a failing scenario can investigate why a row isn't claimed.
		var supLogger shared.Logger = shared.SilentLogger{}
		if os.Getenv("SCENARIO_DEBUG") != "" {
			supLogger = shared.NewSlogLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
		}
		sv, err := config.StartSupervisor(config.SupervisorConfig{
			SupervisorID:                "scenario-supervisor",
			Driver:                      driver,
			Clock:                       clock,
			Logger:                      supLogger,
			Concurrency:                 4,
			HeartbeatInterval:           heartbeatInterval,
			ClaimPollInterval:           100 * time.Millisecond,
			Resolver:                    resolver,
			Stores:                      opts.Stores,
			NamedLocks:                  opts.NamedLocks,
			CallbackHost:                "127.0.0.1",
			CallbackPort:                0,
			ExpectedAttributesSchemaFor: expectedSchemaFor,
			Executors:                   executorsCfg,
			LateBindServiceProxies:      opts.LateBindServiceProxies,
			LifecyclePeersForSpec:       peersForSpec,
		})
		if err != nil {
			t.Fatalf("scenario: start supervisor: %v", err)
		}
		t.Cleanup(func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = sv.Shutdown(sctx)
		})
		h.Supervisor = sv
	}

	ca, err := config.StartControlAPI(config.ControlAPIConfig{
		Driver:                 driver,
		Clock:                  clock,
		Logger:                 shared.SilentLogger{},
		Host:                   "127.0.0.1",
		Port:                   0,
		Stores:                 opts.Stores,
		NamedLocks:             opts.NamedLocks,
		Executors:              executorsCfg,
		LateBindServiceProxies: opts.LateBindServiceProxies,
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

// DeployTemplate marshals the spec into the wrapped POST /templates body
// (`{spec: {...}}`) the control API requires post-control-plane v1,
// registers it, and then POSTs /templates/{id}/deploy to transition
// state to 'deployed'. Returns the template content hash.
func (h *Harness) DeployTemplate(spec node.TemplateSpec) string {
	h.T.Helper()
	if spec.FrameResolutionMode == "" {
		spec.FrameResolutionMode = node.FrameResolutionSerialQueue
	}
	body, err := json.Marshal(map[string]any{
		"spec": templateSpecToJSON(spec),
	})
	if err != nil {
		h.T.Fatal(err)
	}
	resp, err := http.Post(h.ControlBase+"/templates", "application/json", bytesReader(body))
	if err != nil {
		h.T.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
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
	if out.TemplateID == "" {
		h.T.Fatalf("DeployTemplate: empty template_id")
	}
	// Transition register → deployed so /instances will accept the id.
	deployURL := h.ControlBase + "/templates/" + out.TemplateID + "/deploy"
	deployResp, err := http.Post(deployURL, "application/json", bytesReader([]byte("{}")))
	if err != nil {
		h.T.Fatal(err)
	}
	defer deployResp.Body.Close()
	if deployResp.StatusCode != http.StatusOK {
		buf := make([]byte, 4096)
		n, _ := deployResp.Body.Read(buf)
		h.T.Fatalf("DeployTemplate: deploy: status %d: %s", deployResp.StatusCode, string(buf[:n]))
	}
	return out.TemplateID
}

// DeployTemplateSpecMap registers + deploys a template from a raw spec map
// (so callers can include fields the typed node.TemplateSpec doesn't model,
// e.g. late_bind_services) under an optional bearer key. Returns the
// template content hash. Used by the host-agent scenario tests, which need
// the late_bind_services field and run against an authenticated control-api.
func (h *Harness) DeployTemplateSpecMap(specMap map[string]any, bearerKey string) string {
	h.T.Helper()
	if _, ok := specMap["frame_resolution_mode"]; !ok {
		specMap["frame_resolution_mode"] = string(node.FrameResolutionSerialQueue)
	}
	body, err := json.Marshal(map[string]any{"spec": specMap})
	if err != nil {
		h.T.Fatal(err)
	}
	regResp := h.authedPost("/templates", bearerKey, body)
	defer regResp.Body.Close()
	if regResp.StatusCode != http.StatusCreated && regResp.StatusCode != http.StatusOK {
		buf := make([]byte, 4096)
		n, _ := regResp.Body.Read(buf)
		h.T.Fatalf("DeployTemplateSpecMap: register status %d: %s", regResp.StatusCode, string(buf[:n]))
	}
	var out struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.NewDecoder(regResp.Body).Decode(&out); err != nil {
		h.T.Fatalf("DeployTemplateSpecMap: decode: %v", err)
	}
	if out.TemplateID == "" {
		h.T.Fatalf("DeployTemplateSpecMap: empty template_id")
	}
	deployResp := h.authedPost("/templates/"+out.TemplateID+"/deploy", bearerKey, []byte("{}"))
	defer deployResp.Body.Close()
	if deployResp.StatusCode != http.StatusOK {
		buf := make([]byte, 4096)
		n, _ := deployResp.Body.Read(buf)
		h.T.Fatalf("DeployTemplateSpecMap: deploy status %d: %s", deployResp.StatusCode, string(buf[:n]))
	}
	return out.TemplateID
}

// authedPost issues a POST with an optional Bearer key. Helper for the
// authenticated-mode scenario flows.
func (h *Harness) authedPost(path, bearerKey string, body []byte) *http.Response {
	h.T.Helper()
	req, err := http.NewRequest(http.MethodPost, h.ControlBase+path, bytesReader(body))
	if err != nil {
		h.T.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerKey != "" {
		req.Header.Set("Authorization", "Bearer "+bearerKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.T.Fatal(err)
	}
	return resp
}

// CreateInstance POSTs to /instances; returns instance_id. An empty
// consumerKey is omitted from the body so the row's instance_key column
// stays NULL (the unique-index sentinel), rather than being persisted as
// the empty string and immediately conflicting on the next call.
func (h *Harness) CreateInstance(templateHash string, consumerKey string, params map[string]any) shared.UUID {
	h.T.Helper()
	return h.CreateInstanceWithOverrides(templateHash, consumerKey, params, nil)
}

// CreateInstanceWithOverrides is the per-instance-attribute-overrides
// variant. Pass a non-nil overrides map to attach an
// `attribute_overrides` blob to the create request. nil overrides
// reproduces CreateInstance's behaviour exactly.
func (h *Harness) CreateInstanceWithOverrides(
	templateHash string,
	consumerKey string,
	params map[string]any,
	attributeOverrides map[string]any,
) shared.UUID {
	h.T.Helper()
	bodyMap := map[string]any{
		"template": templateHash,
		"params":   params,
	}
	if consumerKey != "" {
		bodyMap["instance_key"] = consumerKey
	}
	if len(attributeOverrides) > 0 {
		bodyMap["attribute_overrides"] = attributeOverrides
	}
	body, err := json.Marshal(bodyMap)
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
	h.waitForRootDispatch(id, 5*time.Second)
	return id
}

// CreateInstanceWithServiceBindings POSTs an instance carrying a
// service_bindings catalog under an authenticated api-key. The bearer key
// is required so the row's created_by_api_key_id (the proxy's agent-routing
// key) is non-null; pass the plaintext minted via MintAdminKey. Returns the
// instance id. Does NOT wait for root dispatch (late-bound dispatch may
// legitimately stall when no agent is connected — the caller asserts the
// terminal/dispatch outcome it expects). Per spec
// 2026-05-24-host-agent-and-proxy-design.md.
func (h *Harness) CreateInstanceWithServiceBindings(
	templateHash, consumerKey, bearerKey string,
	params map[string]any,
	serviceBindings map[string]any,
) shared.UUID {
	h.T.Helper()
	bodyMap := map[string]any{
		"template": templateHash,
		"params":   params,
	}
	if consumerKey != "" {
		bodyMap["instance_key"] = consumerKey
	}
	if len(serviceBindings) > 0 {
		bodyMap["service_bindings"] = serviceBindings
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		h.T.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, h.ControlBase+"/instances", bytesReader(body))
	if err != nil {
		h.T.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerKey != "" {
		req.Header.Set("Authorization", "Bearer "+bearerKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.T.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		h.T.Fatalf("CreateInstanceWithServiceBindings: status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		h.T.Fatalf("CreateInstanceWithServiceBindings: decode: %v", err)
	}
	id, err := parseUUIDStr(out.InstanceID)
	if err != nil {
		h.T.Fatalf("CreateInstanceWithServiceBindings: bad instance_id %q: %v", out.InstanceID, err)
	}
	return id
}

// MintAdminKey mints a full-access admin api-key via the anonymous-mode
// bootstrap path (POST /auth/keys with no bearer). Returns (plaintext,
// keyID). The keyID is the UUID stamped on created_by_api_key_id; the
// host-agent registers with the keyID as its routing key so the proxy can
// match dispatches to the connected agent. Per spec
// 2026-05-24-host-agent-and-proxy-design.md.
func (h *Harness) MintAdminKey(name string) (plaintext, keyID string) {
	h.T.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":        name,
		"permissions": []map[string]any{{"action": "*"}},
	})
	resp, err := http.Post(h.ControlBase+"/auth/keys", "application/json", bytesReader(body))
	if err != nil {
		h.T.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		h.T.Fatalf("MintAdminKey: status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out struct {
		ID        string `json:"id"`
		Plaintext string `json:"plaintext"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		h.T.Fatalf("MintAdminKey: decode: %v", err)
	}
	if out.Plaintext == "" || out.ID == "" {
		h.T.Fatalf("MintAdminKey: missing id/plaintext in response")
	}
	return out.Plaintext, out.ID
}

func (h *Harness) waitForRootDispatch(instanceID shared.UUID, timeout time.Duration) {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		err := h.Pool.QueryRow(h.Ctx, `
            SELECT count(*) FROM rimsky_node_runs d
            JOIN rimsky_nodes n ON n.id = d.node_id
            WHERE n.instance_id = $1
        `, instanceID).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		if h.Scheduler == nil {
			h.driveFrameAndEnqueue(instanceID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (h *Harness) driveFrameAndEnqueue(instanceID shared.UUID) {
	h.T.Helper()
	_ = frame.RunTick(h.Ctx, h.Persist, h.Queue,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	var rows []persistence.NodeRow
	if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.Nodes().ListReadyForDispatch(ctx, tx)
		rows = r
		return err
	}); err != nil {
		return
	}
	for _, n := range rows {
		if n.InstanceID != instanceID || n.FrameID == nil {
			continue
		}
		_ = h.Queue.Enqueue(h.Ctx, persistence.DispatchRequest{
			NodeID:         n.ID,
			ExecutorName:   n.Executor,
			RequiredStores: []string{},
			EnqueuedAt:     time.Now(),
			FrameID:        *n.FrameID,
		})
	}
}

// WaitForNodeState polls the node row until state matches or timeout
// elapses.
func (h *Harness) WaitForNodeState(nodeID shared.UUID, state cascade.NodeState, timeout time.Duration) bool {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	requireRun := state == cascade.NodeStateFresh
	for time.Now().Before(deadline) {
		var n *persistence.NodeRow
		if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(ctx, nodeID, tx)
			n = r
			return err
		}); err != nil && h.T != nil {
			// Test poller: log via the test logger so a transient row-load
			// failure surfaces in test output but does not abort the poll.
			h.T.Logf("WaitForNodeState: load node %s failed; retrying next poll: %v", nodeID.String(), err)
		}
		if n != nil && n.State == state {
			if !requireRun || h.hasRunEvent(nodeID) {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (h *Harness) hasRunEvent(nodeID shared.UUID) bool {
	var count int
	// Pass 5 retired the fixed-string audit kinds; the canonical
	// signal-shaped audit row for a settled-fresh terminal is
	// `terminal/success` per concept:signal. Pure-cascade transitions
	// also emit `terminal/success` (see graph/scheduler/pure_cascade.go).
	err := h.Pool.QueryRow(h.Ctx, `
        SELECT count(*) FROM rimsky_events
        WHERE node_id = $1 AND kind = 'terminal/success'
    `, nodeID).Scan(&count)
	return err == nil && count > 0
}

// WaitForEventKind polls rimsky_events for any row with the given
// (node, kind) pair.
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

// WaitForDispatch polls until a rimsky_node_runs row exists for the
// given node.
func (h *Harness) WaitForDispatch(nodeID shared.UUID, timeout time.Duration) bool {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`, nodeID,
		).Scan(&count)
		if err == nil && count > 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// WaitForWorkerRequestDeleted polls until no in-flight rimsky_node_runs
// rows remain for the given node. Polling on the in-flight-phase
// predicate is the right shape for "this dispatch has retired": post
// the 2026-05-21 lifecycle reorder, the apply* terminal functions
// flip the run row's phase to terminal inside their own tx (alongside
// the node's state update), so by the time a state-transition event
// is observable the queue row is already in 'completed'/'failed'.
// Polling here keeps the helper robust against any future terminal
// path that doesn't yet flip in-tx.
//
// Post-stage-1 lifecycle flip: terminal rows (phase IN
// ('completed','failed')) survive past active terminal so frame-end +
// retention + run-tree aggregation can read their terminal state. The
// "deleted" semantic this helper preserves is "no longer in flight" —
// the in-flight predicate filters on the active phases only.
func (h *Harness) WaitForWorkerRequestDeleted(nodeID shared.UUID, timeout time.Duration) bool {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_node_runs
			  WHERE node_id = $1
			    AND phase IN ('pending','active','held','parked')`, nodeID,
		).Scan(&count)
		if err == nil && count == 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// InTx runs fn inside a fresh Persist.Transaction. Test convenience
// wrapper — option C requires every persistence call to thread an
// explicit tx, so scenario tests run their reads inside one of these.
func (h *Harness) InTx(fn func(tx persistence.Tx) error) error {
	return h.Persist.Transaction(h.Ctx, func(_ context.Context, tx persistence.Tx) error {
		return fn(tx)
	})
}

// GetMainRunScopeID returns the main RunScope id for an instance.
// Convenience wrapper for scenario tests that need to pass the
// `runScopeID` argument to per-run-keyed accessors like
// `NodeAttributes().GetLatestByNode` or `Nodes().UpdateState`.
//
// @concept: run-scope
func (h *Harness) GetMainRunScopeID(instanceID shared.UUID) shared.UUID {
	h.T.Helper()
	var out shared.UUID
	err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.Persist.Instances().Get(ctx, instanceID, tx)
		if err != nil {
			return err
		}
		if row == nil {
			h.T.Fatalf("GetMainRunScopeID: instance %s not found", instanceID)
			return nil
		}
		out = row.MainRunScopeID
		return nil
	})
	if err != nil {
		h.T.Fatalf("GetMainRunScopeID: %v", err)
	}
	return out
}

// GetNodes fetches all nodes for an instance.
func (h *Harness) GetNodes(instanceID shared.UUID) []persistence.NodeRow {
	h.T.Helper()
	var nodes []persistence.NodeRow
	err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.Nodes().ListByInstance(ctx, instanceID, tx)
		nodes = r
		return err
	})
	if err != nil {
		h.T.Fatalf("GetNodes: %v", err)
	}
	return nodes
}

// FindNode returns the first node in the instance matching nodeType,
// or nil.
func (h *Harness) FindNode(instanceID shared.UUID, nodeType string) *persistence.NodeRow {
	for _, n := range h.GetNodes(instanceID) {
		if n.NodeType == nodeType {
			n := n
			return &n
		}
	}
	return nil
}

// templateSpecToJSON converts a node.TemplateSpec into the snake_case
// JSON shape expected by POST /templates.
//
// When the spec carries a non-empty `graphs:` block, the serializer
// emits the nested form and omits the legacy flat `nodes:` field —
// the canonicalizer rejects templates that declare both. When `graphs:`
// is empty, the legacy flat form is emitted unchanged.
func templateSpecToJSON(spec node.TemplateSpec) map[string]any {
	out := map[string]any{
		"name":                  spec.Name,
		"version":               spec.Version,
		"frame_resolution_mode": spec.FrameResolutionMode,
	}
	if len(spec.Graphs) > 0 {
		graphs := make([]map[string]any, 0, len(spec.Graphs))
		for _, g := range spec.Graphs {
			graphs = append(graphs, graphSpecToJSON(g))
		}
		out["graphs"] = graphs
	} else {
		nodes := make([]map[string]any, 0, len(spec.Nodes))
		for _, n := range spec.Nodes {
			nodes = append(nodes, templateNodeToJSON(n))
		}
		out["nodes"] = nodes
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
	if spec.Defaults != nil && spec.Defaults.Attributes != nil && len(spec.Defaults.Attributes.ByExecutor) > 0 {
		out["defaults"] = map[string]any{
			"attributes": map[string]any{
				"by_executor": spec.Defaults.Attributes.ByExecutor,
			},
		}
	}
	return out
}

// graphSpecToJSON serializes one GraphSpec (the nested `graphs:` form
// per spec §Sub-graphs). The reserved `main` graph omits `entry:` /
// `exit:`; sub-graphs MUST declare both — the canonicalizer rejects
// missing values, so the serializer emits whatever the in-memory
// spec carries and lets the rejection happen on the receiver.
func graphSpecToJSON(g node.GraphSpec) map[string]any {
	nodes := make([]map[string]any, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes = append(nodes, templateNodeToJSON(n))
	}
	out := map[string]any{
		"name":  g.Name,
		"nodes": nodes,
	}
	if g.Entry != "" {
		out["entry"] = g.Entry
	}
	if g.Exit != "" {
		out["exit"] = g.Exit
	}
	return out
}

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
	if len(n.Subscribes) > 0 {
		subs := make([]map[string]any, 0, len(n.Subscribes))
		for _, s := range n.Subscribes {
			item := map[string]any{"type": s.Type}
			if s.Node != "" {
				item["node"] = s.Node
			}
			if s.Instance {
				item["instance"] = true
			}
			if s.When != "" {
				item["when"] = s.When
			}
			if s.Frame != "" {
				item["frame"] = s.Frame
			}
			subs = append(subs, item)
		}
		nd["subscribes"] = subs
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
			locks = append(locks, map[string]any{"name": l.Name})
		}
		nd["locks"] = locks
	}
	if n.Attributes != nil && len(n.Attributes.Schema) > 0 {
		nd["attributes"] = map[string]any{"schema": n.Attributes.Schema}
	}
	if len(n.Inherits) > 0 {
		ihs := make([]map[string]any, 0, len(n.Inherits))
		for _, ie := range n.Inherits {
			ihs = append(ihs, map[string]any{"claim": ie.Claim})
		}
		nd["inherits"] = ihs
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
				if a.ReasonTemplate != "" {
					act["reason_template"] = a.ReasonTemplate
				}
				actions = append(actions, act)
			}
			ets[cls] = map[string]any{"policy": actions}
		}
		nd["error_types"] = ets
	}
	// Lifecycle-handler slots (on_acquire_unavailable,
	// on_executor_complete, on_executor_errored) retired 2026-05-23
	// per .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-
	// decoupling-design.md. Their behaviors fold into error_types:
	// (acquisition failure / pass-on-error) and into receiver-side
	// CEL when: predicates (cascade selectivity on payload.changed).
	if n.MaxParkDuration != "" {
		nd["max_park_duration"] = n.MaxParkDuration
	}
	if n.MaxRetriesWithoutProgress != nil {
		nd["max_retries_without_progress"] = *n.MaxRetriesWithoutProgress
	}
	if n.FanOut != nil {
		nd["fan_out"] = fanOutSpecToJSON(n.FanOut)
	}
	if n.Delegate != "" {
		nd["delegate"] = n.Delegate
	}
	if len(n.Holds) > 0 {
		holds := map[string]any{}
		for alias, binding := range n.Holds {
			entry := map[string]any{"from": binding.From}
			if binding.As != "" {
				entry["as"] = binding.As
			}
			holds[alias] = entry
		}
		nd["holds"] = holds
	}
	return nd
}

// fanOutSpecToJSON serializes a FanOutSpec into the snake_case shape
// the control-api template registrar accepts. Mirrors the template-DSL
// shape per spec §Fan-out template DSL.
func fanOutSpecToJSON(fo *node.FanOutSpec) map[string]any {
	out := map[string]any{
		"claim":             fo.Claim,
		"partition_request": fo.PartitionRequest,
	}
	if fo.Parallelism > 0 {
		out["parallelism"] = fo.Parallelism
	}
	policy := map[string]any{}
	if fo.ErrorPolicy.Kind != "" {
		policy["kind"] = fo.ErrorPolicy.Kind
	}
	if fo.ErrorPolicy.MaxFailures > 0 {
		policy["max_failures"] = fo.ErrorPolicy.MaxFailures
	}
	if fo.ErrorPolicy.CancelSiblings {
		policy["cancel_siblings"] = fo.ErrorPolicy.CancelSiblings
	}
	out["error_policy"] = policy
	return out
}

func storeRefToJSON(s node.NodeStoreRef) map[string]any {
	item := map[string]any{
		"name":     s.Name,
		"selector": s.Selector,
		"intent":   s.Intent,
	}
	if s.Alias != "" {
		item["alias"] = s.Alias
	}
	return item
}

// withStores / withLocks / etc — fluent option helpers.

func withStores(refs ...node.NodeStoreRef) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Stores = append(n.Stores, refs...)
	}
}

func withLocks(refs ...node.NodeLockRef) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Locks = append(n.Locks, refs...)
	}
}

func withAttributes(schema map[string]any) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Attributes = &node.NodeAttributesDef{Schema: schema}
	}
}

func withInherits(refs ...node.InheritEntry) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Inherits = append(n.Inherits, refs...)
	}
}

func withSubscribes(subs ...node.SubscriptionEntry) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Subscribes = append(n.Subscribes, subs...)
	}
}

// ExecSQL runs a raw SQL command against the harness's underlying
// Postgres pool. Test-only escape hatch for scenario tests that seed
// fixtures via raw SQL — equivalent to h.Pool.Exec but does not force
// callers to import pgxpool. Fatals on SQL error.
func (h *Harness) ExecSQL(sql string, args ...any) {
	h.T.Helper()
	if _, err := h.Pool.Exec(h.Ctx, sql, args...); err != nil {
		h.T.Fatalf("scenario.Harness.ExecSQL: %v\nsql: %s", err, sql)
	}
}

// QueryRowSQL runs a raw SQL SELECT and scans the single returned row
// into dest. Test-only escape hatch in the same vein as ExecSQL.
// Fatals on SQL error.
func (h *Harness) QueryRowSQL(sql string, args []any, dest ...any) {
	h.T.Helper()
	if err := h.Pool.QueryRow(h.Ctx, sql, args...).Scan(dest...); err != nil {
		h.T.Fatalf("scenario.Harness.QueryRowSQL: %v\nsql: %s", err, sql)
	}
}

// QuerySQL runs a raw SQL SELECT and invokes scan on each row. The
// scan callback receives a closure that mirrors pgx.Rows.Scan; the test
// can call it once per column-set without importing pgx itself.
// Fatals on SQL error.
func (h *Harness) QuerySQL(sql string, args []any, scan func(scan func(...any) error) error) {
	h.T.Helper()
	rows, err := h.Pool.Query(h.Ctx, sql, args...)
	if err != nil {
		h.T.Fatalf("scenario.Harness.QuerySQL: %v\nsql: %s", err, sql)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows.Scan); err != nil {
			h.T.Fatalf("scenario.Harness.QuerySQL: scan: %v\nsql: %s", err, sql)
		}
	}
	if err := rows.Err(); err != nil {
		h.T.Fatalf("scenario.Harness.QuerySQL: rows: %v\nsql: %s", err, sql)
	}
}

// MakeNode constructs a TemplateNodeDef by applying option helpers.
func MakeNode(base node.TemplateNodeDef, opts ...func(*node.TemplateNodeDef)) node.TemplateNodeDef {
	n := base
	for _, o := range opts {
		o(&n)
	}
	return n
}

// WithStores / WithLocks / WithAttributes / WithInherits — exported
// aliases for the option helpers above.
func WithStores(refs ...node.NodeStoreRef) func(*node.TemplateNodeDef) {
	return withStores(refs...)
}

func WithLocks(refs ...node.NodeLockRef) func(*node.TemplateNodeDef) {
	return withLocks(refs...)
}

func WithAttributes(schema map[string]any) func(*node.TemplateNodeDef) {
	return withAttributes(schema)
}

func WithInherits(refs ...node.InheritEntry) func(*node.TemplateNodeDef) {
	return withInherits(refs...)
}

// WithSubscribes appends one or more SubscriptionEntry receivers to the
// node's Subscribes list. Used by scenario tests to declare cascade
// coupling under the post-2026-05-14 subscription model.
func WithSubscribes(subs ...node.SubscriptionEntry) func(*node.TemplateNodeDef) {
	return withSubscribes(subs...)
}

// WithFanOut attaches a FanOutSpec to the node. Used by scenario tests
// that exercise the §Fan-out template DSL acquisition + dispatch path.
func WithFanOut(fo *node.FanOutSpec) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.FanOut = fo
	}
}
