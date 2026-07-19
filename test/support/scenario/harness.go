// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/graph/scheduler"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	stubexec "github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
	stubtest "github.com/rimsky-ai/rimsky-core/test/support/executors/stub/stubtest"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

type Harness struct {
	T                  testing.TB
	Ctx                context.Context
	LastDeployWarnings []string
	Pool               *pgxpool.Pool
	Driver             persistence.Database
	Persist            persistence.Tables
	Queue              persistence.Queue
	Stub               *stubexec.Stub
	StubAddr           string
	Scheduler          config.SchedulerHandle
	Supervisor         config.SupervisorHandle
	ControlAPI         config.ControlAPIHandle
	ControlBase        string
	Clock              shared.Clock
}

type HarnessOpts struct {
	ExtraExecutors map[string]executor.Endpoint

	NoSupervisor bool

	NoScheduler bool

	SchedulerTick time.Duration

	LivenessInterval time.Duration

	Clock shared.Clock

	ClaimProducers config.RemoteClaimProducersConfig

	Publishers config.RemotePublishersConfig

	NamedLocks locks.NamedLocksConfig

	LateBindServiceProxies map[string]string

	ExecutorProtocols map[string][]string

	RefValidationMode node.RefValidationMode

	// @concept: executor
	ExtraInprocHandlers map[string]executor.InProcessHandler

	Deadline time.Duration
}

const defaultHarnessDeadline = 90 * time.Second

func Start(t testing.TB, opts HarnessOpts) *Harness {
	t.Helper()
	deadline := opts.Deadline
	if deadline <= 0 {
		deadline = defaultHarnessDeadline
	}
	ctx, cancelDeadline := context.WithTimeout(context.Background(), deadline)
	t.Cleanup(cancelDeadline)

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

	livenessInterval := opts.LivenessInterval
	if livenessInterval == 0 {
		livenessInterval = 500 * time.Millisecond
	}
	maxQuietPeriod := 5 * livenessInterval
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
			Driver:                driver,
			Clock:                 clock,
			Logger:                shared.SilentLogger{},
			TickInterval:          schedulerTick,
			MaxQuietPeriodDefault: maxQuietPeriod,
			ClaimProducers:        opts.ClaimProducers,
			NamedLocks:            opts.NamedLocks,
			SupervisorID:          "scenario-scheduler",
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

	expectedSchemaFor := func(executorName string) ([]byte, bool) {
		if _, known := executors[executorName]; !known {
			return nil, false
		}
		return []byte(`{"type":"object"}`), true
	}

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
			LivenessInterval:            livenessInterval,
			ClaimPollInterval:           100 * time.Millisecond,
			Resolver:                    resolver,
			ClaimProducers:              opts.ClaimProducers,
			NamedLocks:                  opts.NamedLocks,
			CallbackHost:                "127.0.0.1",
			CallbackPort:                0,
			ExpectedAttributesSchemaFor: expectedSchemaFor,
			Executors:                   executorsCfg,
			LateBindServiceProxies:      opts.LateBindServiceProxies,
			LifecyclePeersForSpec:       peersForSpec,
			ExtraInprocHandlers:         opts.ExtraInprocHandlers,
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
		ClaimProducers:         opts.ClaimProducers,
		Publishers:             opts.Publishers,
		NamedLocks:             opts.NamedLocks,
		Executors:              executorsCfg,
		LateBindServiceProxies: opts.LateBindServiceProxies,
		RefValidationMode:      opts.RefValidationMode,
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

func (h *Harness) DeployTemplate(spec node.TemplateSpec) string {
	h.T.Helper()
	body, err := json.Marshal(map[string]any{
		"spec": templateSpecToJSON(spec),
	})
	if err != nil {
		h.T.Fatal(err)
	}
	resp, err := http.Post(h.ControlBase+"/v1/templates", "application/json", bytesReader(body))
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
		TemplateID         string `json:"template_id"`
		ValidationWarnings []struct {
			ServiceName string `json:"service_name"`
			Role        string `json:"role"`
			NodeAlias   string `json:"node_alias,omitempty"`
			Class       string `json:"class,omitempty"`
			Message     string `json:"message,omitempty"`
			Path        string `json:"path,omitempty"`
		} `json:"validation_warnings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		h.T.Fatalf("DeployTemplate: decode: %v", err)
	}
	if out.TemplateID == "" {
		h.T.Fatalf("DeployTemplate: empty template_id")
	}
	h.LastDeployWarnings = h.LastDeployWarnings[:0]
	for _, w := range out.ValidationWarnings {
		h.LastDeployWarnings = append(h.LastDeployWarnings, w.Message+" "+w.Path)
	}
	deployURL := h.ControlBase + "/v1/templates/" + out.TemplateID + "/deploy"
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

func (h *Harness) DeployTemplateSpecMap(specMap map[string]any, bearerKey string) string {
	h.T.Helper()
	body, err := json.Marshal(map[string]any{"spec": specMap})
	if err != nil {
		h.T.Fatal(err)
	}
	regResp := h.authedPost("/v1/templates", bearerKey, body)
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
	deployResp := h.authedPost("/v1/templates/"+out.TemplateID+"/deploy", bearerKey, []byte("{}"))
	defer deployResp.Body.Close()
	if deployResp.StatusCode != http.StatusOK {
		buf := make([]byte, 4096)
		n, _ := deployResp.Body.Read(buf)
		h.T.Fatalf("DeployTemplateSpecMap: deploy status %d: %s", deployResp.StatusCode, string(buf[:n]))
	}
	return out.TemplateID
}

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

func (h *Harness) CreateInstance(templateHash string, consumerKey string, params map[string]any) shared.UUID {
	h.T.Helper()
	return h.CreateInstanceWithOverrides(templateHash, consumerKey, params, nil)
}

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
	resp, err := http.Post(h.ControlBase+"/v1/instances", "application/json", bytesReader(body))
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
	// @decision: test-harness-create-instance-wakes-roots-after-create
	if h.templateHasStructuralRoot(templateHash) {
		h.PostInstanceMessage(id, "", nil, "harness-wake-create-"+id.String())
		h.waitForRootDispatch(id)
	}
	return id
}

// @decision: test-harness-create-instance-wakes-roots-after-create
// @decision: test-harness-invalidate-node-retired
func (h *Harness) PostInstanceMessage(instanceID shared.UUID, msgType string, payload []byte, idempotencyKey string) shared.UUID {
	return h.PostInstanceMessageWithAuth(instanceID, msgType, payload, idempotencyKey, "")
}

// @decision: test-harness-create-instance-wakes-roots-after-create
// @decision: test-harness-invalidate-node-retired
func (h *Harness) PostInstanceMessageWithAuth(instanceID shared.UUID, msgType string, payload []byte, idempotencyKey, bearerKey string) shared.UUID {
	h.T.Helper()
	bodyMap := map[string]any{"type": msgType}
	if len(payload) > 0 {
		bodyMap["payload"] = json.RawMessage(payload)
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		h.T.Fatal(err)
	}
	url := h.ControlBase + "/v1/instances/" + instanceID.String() + "/messages"
	req, err := http.NewRequest(http.MethodPost, url, bytesReader(body))
	if err != nil {
		h.T.Fatalf("PostInstanceMessage: build: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	if bearerKey != "" {
		req.Header.Set("Authorization", "Bearer "+bearerKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.T.Fatalf("PostInstanceMessage: post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		h.T.Fatalf("PostInstanceMessage: status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out struct {
		MessageID string `json:"message_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		h.T.Fatalf("PostInstanceMessage: decode: %v", err)
	}
	id, err := parseUUIDStr(out.MessageID)
	if err != nil {
		h.T.Fatalf("PostInstanceMessage: bad message_id %q: %v", out.MessageID, err)
	}
	return id
}

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
	req, err := http.NewRequest(http.MethodPost, h.ControlBase+"/v1/instances", bytesReader(body))
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
	// @decision: test-harness-create-instance-wakes-roots-after-create
	if h.templateHasStructuralRoot(templateHash) {
		h.PostInstanceMessageWithAuth(id, "", nil, "harness-wake-create-"+id.String(), bearerKey)
		h.waitForRootDispatch(id)
	}
	return id
}

func (h *Harness) MintAdminKey(name string) (plaintext, keyID string) {
	h.T.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":        name,
		"permissions": []map[string]any{{"action": "*"}},
	})
	resp, err := http.Post(h.ControlBase+"/v1/auth/keys", "application/json", bytesReader(body))
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

// @decision: structural-root-edge-injection-at-registration
// @decision: test-harness-create-instance-wakes-roots-after-create
func (h *Harness) templateHasStructuralRoot(templateHash string) bool {
	h.T.Helper()
	var tmplSpec *persistence.TemplateRow
	err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := h.Persist.Templates().GetByHash(ctx, templateHash, tx)
		tmplSpec = row
		return err
	})
	if err != nil || tmplSpec == nil {
		return true
	}
	msgRefs := node.ExtractMessageRefsFromTemplate(tmplSpec.Spec)
	edges, err := node.BuildSubscriptionEdges(tmplSpec.Spec, msgRefs)
	if err != nil || edges == nil {
		return true
	}
	matched := edges.Match("", signal.TypePath("terminal/success"))
	return len(matched) > 0
}

func (h *Harness) waitForRootDispatch(instanceID shared.UUID) {
	h.T.Helper()
	for {
		var count int
		err := h.Pool.QueryRow(h.Ctx, `
            SELECT count(*) FROM rimsky_node_runs d
            JOIN rimsky_nodes n ON n.id = d.node_id
            WHERE n.instance_id = $1
              AND n.node_type != ''
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
	silentLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = frame.RunTick(h.Ctx, h.Persist, h.Queue, silentLogger)
	// @decision: empty-message-as-root-trigger
	_ = runtime.SweepDeliverMessagesForRunningFrames(h.Ctx, h.Persist,
		shared.SilentLogger{}, time.Now())
	_, _ = scheduler.ProcessPureCascade(h.Ctx, scheduler.PureCascadeArgs{
		Persist: h.Persist, Queue: h.Queue, Clock: shared.SystemClock{},
		Logger: shared.SilentLogger{},
	})
	_ = frame.RunTick(h.Ctx, h.Persist, h.Queue, silentLogger)
	var (
		rows           []persistence.NodeRow
		runningFrameID *shared.UUID
	)
	if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.Nodes().ListReadyForDispatch(ctx, tx)
		if err != nil {
			return err
		}
		rows = r
		fr, err := h.Persist.Frames().GetRunningFrameID(ctx, instanceID, tx)
		if err != nil {
			return err
		}
		runningFrameID = fr
		return nil
	}); err != nil {
		return
	}
	if runningFrameID == nil {
		return
	}
	for _, n := range rows {
		if n.InstanceID != instanceID {
			continue
		}
		_ = h.Queue.Enqueue(h.Ctx, persistence.DispatchRequest{
			NodeID:                 n.ID,
			ExecutorName:           n.Executor,
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now(),
			FrameID:                *runningFrameID,
		})
	}
}

// @concept: node-run
func (h *Harness) nodeReachedState(nodeID shared.UUID, state cascade.NodeState) bool {
	var latest *persistence.NodeRunLatest
	if err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		l, err := h.Persist.Nodes().GetLatestRunForNode(ctx, tx, nodeID)
		latest = l
		return err
	}); err != nil && h.T != nil {
		h.T.Logf("WaitForNodeState: latest-run probe for %s failed; retrying next poll: %v", nodeID.String(), err)
	}
	if latest != nil && latest.State == state {
		if state != cascade.NodeStateFresh || h.hasRunEvent(nodeID) {
			return true
		}
	}
	return false
}

func (h *Harness) WaitForNodeState(nodeID shared.UUID, state cascade.NodeState) {
	h.T.Helper()
	for !h.nodeReachedState(nodeID, state) {
		time.Sleep(50 * time.Millisecond)
	}
}

func (h *Harness) hasRunEvent(nodeID shared.UUID) bool {
	var count int
	err := h.Pool.QueryRow(h.Ctx, `
        SELECT count(*) FROM rimsky_events
        WHERE node_id = $1 AND kind = 'terminal/success'
    `, nodeID).Scan(&count)
	return err == nil && count > 0
}

func (h *Harness) HasEventKind(nodeID shared.UUID, kind string) bool {
	h.T.Helper()
	var count int
	err := h.Pool.QueryRow(h.Ctx, `
        SELECT count(*) FROM rimsky_events
        WHERE node_id = $1 AND kind = $2
    `, nodeID, kind).Scan(&count)
	return err == nil && count > 0
}

func (h *Harness) WaitForEventKind(nodeID shared.UUID, kind string) {
	h.T.Helper()
	for {
		if h.HasEventKind(nodeID, kind) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (h *Harness) WaitForDispatch(nodeID shared.UUID) {
	h.T.Helper()
	for {
		var count int
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1`, nodeID,
		).Scan(&count)
		if err == nil && count > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// @concept: node-run
func (h *Harness) WaitForAllRunsTerminal(nodeID shared.UUID) {
	h.T.Helper()
	for {
		var count int
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_node_runs
			  WHERE node_id = $1
			    AND state IN ('pending','stale','running','held','parked')`, nodeID,
		).Scan(&count)
		if err == nil && count == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (h *Harness) InTx(fn func(tx persistence.Tx) error) error {
	return h.Persist.Transaction(h.Ctx, func(_ context.Context, tx persistence.Tx) error {
		return fn(tx)
	})
}

// @concept: frame
// @decision: frame-isolation-is-structural
func (h *Harness) GetRunningFrameID(instanceID shared.UUID) shared.UUID {
	h.T.Helper()
	var out shared.UUID
	err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		fr, err := h.Persist.Frames().GetRunningFrameID(ctx, instanceID, tx)
		if err != nil {
			return err
		}
		if fr == nil {
			h.T.Fatalf("GetRunningFrameID: no running frame for instance %s", instanceID)
			return nil
		}
		out = *fr
		return nil
	})
	if err != nil {
		h.T.Fatalf("GetRunningFrameID: %v", err)
	}
	return out
}

// @concept: run-scope
func (h *Harness) GetMainRunScopeID(instanceID shared.UUID) shared.UUID {
	h.T.Helper()
	var out shared.UUID
	instArg := instanceID
	err := h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		page, err := h.Persist.Frames().ListForObservability(ctx,
			persistence.FrameListFilter{InstanceID: &instArg},
			persistence.ListPagination{Limit: 1}, tx)
		if err != nil {
			return err
		}
		if len(page.Rows) == 0 {
			h.T.Fatalf("GetMainRunScopeID: no frames for instance %s", instanceID)
			return nil
		}
		out = page.Rows[0].RootRunScopeID
		return nil
	})
	if err != nil {
		h.T.Fatalf("GetMainRunScopeID: %v", err)
	}
	return out
}

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

func (h *Harness) FindNode(instanceID shared.UUID, nodeType string) *persistence.NodeRow {
	for _, n := range h.GetNodes(instanceID) {
		if n.NodeType == nodeType {
			n := n
			return &n
		}
	}
	return nil
}

func templateSpecToJSON(spec node.TemplateSpec) map[string]any {
	out := map[string]any{
		"name":    spec.Name,
		"version": spec.Version,
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
	if len(spec.Messages) > 0 {
		msgs := make([]map[string]any, 0, len(spec.Messages))
		for _, m := range spec.Messages {
			item := map[string]any{"type": m.Type}
			if len(m.BodySchema) > 0 {
				var schema any
				if err := json.Unmarshal(m.BodySchema, &schema); err == nil {
					item["body_schema"] = schema
				}
			}
			msgs = append(msgs, item)
		}
		out["messages"] = msgs
	}
	if spec.MessageQueueMode != "" {
		out["message_queue_mode"] = spec.MessageQueueMode
	}
	if len(spec.Publishers) > 0 {
		pubs := make([]map[string]any, 0, len(spec.Publishers))
		for _, p := range spec.Publishers {
			pubs = append(pubs, publisherSpecToJSON(p))
		}
		out["publishers"] = pubs
	}
	return out
}

func publisherSpecToJSON(p node.PublisherSpec) map[string]any {
	cfg := any(map[string]any{})
	if len(p.Config) > 0 {
		var decoded any
		if err := json.Unmarshal(p.Config, &decoded); err == nil {
			cfg = decoded
		}
	}
	out := map[string]any{
		"name":   p.Name,
		"kind":   p.Kind,
		"config": cfg,
	}
	if p.MessageType != "" {
		out["message_type"] = p.MessageType
	}
	return out
}

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
	if n.Kind != "" {
		nd["kind"] = n.Kind
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
			if s.When != "" {
				item["when"] = s.When
			}
			if s.ForceUpstreamRefresh != nil {
				item["force_upstream_refresh"] = *s.ForceUpstreamRefresh
			}
			subs = append(subs, item)
		}
		nd["subscribes"] = subs
	}
	if len(n.ClaimProducers) > 0 {
		stores := make([]map[string]any, 0, len(n.ClaimProducers))
		for _, s := range n.ClaimProducers {
			stores = append(stores, claimProducerRefToJSON(s))
		}
		nd["claim_producers"] = stores
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
	if len(n.ErrorTypes) > 0 {
		ets := map[string]any{}
		for cls, etp := range n.ErrorTypes {
			entry := map[string]any{"action": etp.Action}
			if etp.ReasonTemplate != "" {
				entry["reason_template"] = etp.ReasonTemplate
			}
			ets[cls] = entry
		}
		nd["error_types"] = ets
	}
	if n.MaxParkDuration != "" {
		nd["max_park_duration"] = n.MaxParkDuration
	}
	if n.MaxRetries != nil {
		nd["max_retries"] = *n.MaxRetries
	}
	if n.RetryBackoff != nil {
		rb := map[string]any{}
		if n.RetryBackoff.Kind != "" {
			rb["kind"] = string(n.RetryBackoff.Kind)
		}
		if n.RetryBackoff.Jitter != "" {
			rb["jitter"] = string(n.RetryBackoff.Jitter)
		}
		if n.RetryBackoff.BaseDelayMs != 0 {
			rb["base_delay_ms"] = n.RetryBackoff.BaseDelayMs
		}
		if n.RetryBackoff.MaxDelayMs != 0 {
			rb["max_delay_ms"] = n.RetryBackoff.MaxDelayMs
		}
		nd["retry_backoff"] = rb
	}
	if n.FanOut != nil {
		nd["fan_out"] = fanOutSpecToJSON(n.FanOut)
	}
	if n.Delegate != "" {
		nd["delegate"] = n.Delegate
	}
	if n.SendsMessage != "" {
		nd["sends_message"] = n.SendsMessage
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
	if n.CascadeMode != "" {
		nd["cascade_mode"] = n.CascadeMode
	}
	return nd
}

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

func claimProducerRefToJSON(s node.NodeClaimProducerRef) map[string]any {
	item := map[string]any{
		"name":     s.Name,
		"selector": s.Selector,
		"intent":   s.Intent,
	}
	if s.Alias != "" {
		item["alias"] = s.Alias
	}
	if s.Lifetime != "" {
		item["lifetime"] = s.Lifetime
	}
	return item
}

func withClaimProducers(refs ...node.NodeClaimProducerRef) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.ClaimProducers = append(n.ClaimProducers, refs...)
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

func withSubscribes(subs ...node.SubscriptionEntry) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.Subscribes = append(n.Subscribes, subs...)
	}
}

func (h *Harness) ExecSQL(sql string, args ...any) {
	h.T.Helper()
	if _, err := h.Pool.Exec(h.Ctx, sql, args...); err != nil {
		h.T.Fatalf("scenario.Harness.ExecSQL: %v\nsql: %s", err, sql)
	}
}

func (h *Harness) QueryRowSQL(sql string, args []any, dest ...any) {
	h.T.Helper()
	if err := h.Pool.QueryRow(h.Ctx, sql, args...).Scan(dest...); err != nil {
		h.T.Fatalf("scenario.Harness.QueryRowSQL: %v\nsql: %s", err, sql)
	}
}

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

func MakeNode(base node.TemplateNodeDef, opts ...func(*node.TemplateNodeDef)) node.TemplateNodeDef {
	n := base
	for _, o := range opts {
		o(&n)
	}
	return n
}

func WithClaimProducers(refs ...node.NodeClaimProducerRef) func(*node.TemplateNodeDef) {
	return withClaimProducers(refs...)
}

func WithLocks(refs ...node.NodeLockRef) func(*node.TemplateNodeDef) {
	return withLocks(refs...)
}

func WithAttributes(schema map[string]any) func(*node.TemplateNodeDef) {
	return withAttributes(schema)
}

func WithSubscribes(subs ...node.SubscriptionEntry) func(*node.TemplateNodeDef) {
	return withSubscribes(subs...)
}

func WithFanOut(fo *node.FanOutSpec) func(*node.TemplateNodeDef) {
	return func(n *node.TemplateNodeDef) {
		n.FanOut = fo
	}
}
