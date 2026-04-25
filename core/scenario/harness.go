// Package scenario provides a full-stack test harness spinning up every
// in-process component (scheduler, supervisor, stub executor, control API)
// against a testcontainers Postgres. Used by test/scenarios/*_test.go.
package scenario

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/config"
	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/queue"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/resource/inlinejsonb"
	"github.com/fallguy/rimsky/core/shared"
	storagepkg "github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/supervisor"
	"github.com/fallguy/rimsky/executors/stub"
)

// Harness bundles every in-process component wired against a single
// testcontainers Postgres instance. All fields are safe to access from the
// test goroutine; background goroutines (scheduler, supervisor loop, HTTP
// servers) are torn down via t.Cleanup hooks registered in Start.
type Harness struct {
	T           testing.TB
	Ctx         context.Context
	Pool        *pgxpool.Pool
	Storage     storagepkg.StorageBackend
	Queue       queue.DispatchQueue
	Stub        *stub.Stub
	StubAddr    string
	Scheduler   config.SchedulerHandle
	Supervisor  *supervisor.Handle
	ControlAPI  config.ControlAPIHandle
	ControlBase string // http://host:port
}

// HarnessOpts tweaks which components the harness starts. Zero value yields
// scheduler + supervisor + stub + control-api wired in the default fast-tick
// configuration used by scenario tests.
type HarnessOpts struct {
	// If non-empty, registers these executor endpoints in addition to the stub.
	ExtraExecutors map[string]executor.Endpoint
	// If true, skips starting the supervisor (for scenarios that drive claims manually).
	NoSupervisor bool
	// If true, skips starting the scheduler (for scenarios that want manual tick control).
	NoScheduler bool
	// Scheduler tick interval; default 250ms for fast tests.
	SchedulerTick time.Duration
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

	// Per-harness factory registry so parallel scenario tests don't alias
	// across each other's storage backends (the global registry would).
	factories := resource.NewRegistry()
	factories.Register("inline-jsonb", inlinejsonb.Factory{StorageRegistry: sb.Resources()})

	// Stub executor.
	s := stub.New()
	_, stubAddr := s.Listen(t)

	executors := map[string]executor.Endpoint{
		"stub":     {Transport: "grpc", URL: stubAddr},
		"testexec": {Transport: "grpc", URL: stubAddr}, // alias used by some scenarios
	}
	for k, v := range opts.ExtraExecutors {
		executors[k] = v
	}
	resolver := executor.NewStaticResolver(executors)

	h := &Harness{T: t, Ctx: ctx, Pool: pool, Storage: sb, Queue: q, Stub: s, StubAddr: stubAddr}

	// Scheduler.
	if !opts.NoScheduler {
		tick := opts.SchedulerTick
		if tick == 0 {
			tick = 250 * time.Millisecond
		}
		sh, err := config.StartScheduler(config.SchedulerConfig{
			Storage:              sb,
			Queue:                q,
			Clock:                shared.SystemClock{},
			Logger:               shared.SilentLogger{},
			TickInterval:         tick,
			HeartbeatTimeout:     5 * time.Second,
			OrphanedClaimTimeout: 25 * time.Second,
			Pool:                 pool,
		})
		if err != nil {
			t.Fatalf("scenario: start scheduler: %v", err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = sh.Shutdown(ctx)
		})
		h.Scheduler = sh
	}

	// Supervisor.
	if !opts.NoSupervisor {
		sv, err := supervisor.Start(supervisor.Config{
			SupervisorID:      "scenario-supervisor",
			Storage:           sb,
			Queue:             q,
			Clock:             shared.SystemClock{},
			Logger:            shared.SilentLogger{},
			Concurrency:       4,
			HeartbeatInterval: 500 * time.Millisecond,
			ClaimPollInterval: 100 * time.Millisecond,
			Resolver:          resolver,
			GetResource: func(ctx context.Context, resourceID shared.UUID) (resource.Resource, error) {
				return getResourceForOwner(ctx, sb, factories, resourceID)
			},
			ResourceFactories: factories,
			CallbackHost:      "127.0.0.1",
			CallbackPort:      0,
		})
		if err != nil {
			t.Fatalf("scenario: start supervisor: %v", err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = sv.Shutdown(ctx)
		})
		h.Supervisor = sv
	}

	// Control API.
	ca, err := config.StartControlAPI(config.ControlAPIConfig{
		Storage:           sb,
		Queue:             q,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		Host:              "127.0.0.1",
		Port:              0,
		ResourceFactories: factories,
	})
	if err != nil {
		t.Fatalf("scenario: start controlapi: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ca.Shutdown(ctx)
	})
	h.ControlAPI = ca
	h.ControlBase = "http://" + ca.Addr()

	return h
}

// getResourceForOwner looks up the resource row by ID and constructs a
// Resource via the harness's explicit factory registry.
//
// v1 note: the lookup keys the harness's single registered implementation
// ("inline-jsonb"). Per-resource implementation switching is a post-v1
// concern tracked in CHANGELOG.
func getResourceForOwner(ctx context.Context, sb storagepkg.StorageBackend, factories *resource.FactoryRegistry, resourceID shared.UUID) (resource.Resource, error) {
	row, err := sb.Resources().Get(ctx, resourceID, nil)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	fac, ok := factories.Get("inline-jsonb")
	if !ok {
		return nil, nil
	}
	cfg := resource.Config{
		"_resource_id":   resourceID.String(),
		"_path":          row.ResourcePath,
		"_owner_node_id": row.OwnerNodeID.String(),
		"keep_versions":  row.KeepVersions,
	}
	return fac.Create(cfg, nil, nil)
}

// DeployTemplate marshals the spec to the control API's JSON schema and POSTs
// to /templates. Returns the new template_id or fails the test on any error.
func (h *Harness) DeployTemplate(spec node.TemplateSpec) shared.UUID {
	h.T.Helper()
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
		h.T.Fatalf("DeployTemplate: status %d", resp.StatusCode)
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
		h.T.Fatalf("CreateInstance: status %d", resp.StatusCode)
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
	return id
}

// WaitForNodeState polls the node row until state matches or timeout elapses.
// Returns true when the state was observed.
func (h *Harness) WaitForNodeState(nodeID shared.UUID, state shared.NodeState, timeout time.Duration) bool {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n, err := h.Storage.Nodes().Get(h.Ctx, nodeID, nil)
		if err == nil && n != nil && n.State == state {
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
// shape expected by POST /templates. We mirror the controlapi request struct
// here rather than import it (internal package) — the fields are stable.
func templateSpecToJSON(spec node.TemplateSpec) map[string]any {
	nodes := make([]map[string]any, 0, len(spec.Nodes))
	for _, n := range spec.Nodes {
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
		if len(n.ConcurrencyTags) > 0 {
			nd["concurrency_tags"] = n.ConcurrencyTags
		}
		if len(n.OwnsResources) > 0 {
			owns := make([]map[string]any, 0, len(n.OwnsResources))
			for _, rd := range n.OwnsResources {
				item := map[string]any{
					"path":           rd.Path,
					"implementation": rd.Implementation,
				}
				if len(rd.Config) > 0 {
					item["config"] = rd.Config
				}
				if rd.Retention != nil {
					item["retention"] = map[string]any{"keep_versions": rd.Retention.KeepVersions}
				}
				owns = append(owns, item)
			}
			nd["owns_resources"] = owns
		}
		if len(n.ReadsResources) > 0 {
			reads := make([]map[string]any, 0, len(n.ReadsResources))
			for _, rr := range n.ReadsResources {
				reads = append(reads, map[string]any{
					"path": rr.Path,
					"via":  string(rr.Via),
				})
			}
			nd["reads_resources"] = reads
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
					if a.RestoreVersion != "" {
						act["restore_version"] = a.RestoreVersion
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
		nodes = append(nodes, nd)
	}
	out := map[string]any{
		"name":    spec.Name,
		"version": spec.Version,
		"nodes":   nodes,
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
