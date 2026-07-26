// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	sharedNetworkOnceReapedByRyukAtProcessExit sync.Once
	sharedNetwork                              *testcontainers.DockerNetwork
	sharedNetworkErr                           error
	aliasCounter                               atomic.Uint64
)

func sharedNetworkName(ctx context.Context) (string, error) {
	sharedNetworkOnceReapedByRyukAtProcessExit.Do(func() {
		sharedNetwork, sharedNetworkErr = tcnet.New(ctx)
	})
	if sharedNetworkErr != nil {
		return "", sharedNetworkErr
	}
	return sharedNetwork.Name, nil
}

func nextAliasSuffix() uint64 {
	return aliasCounter.Add(1)
}

const rimskyAllImage = "rimsky-all-in-one"

const healthPollInterval = 500 * time.Millisecond

type RimskyEndpoint struct {
	BaseURL         string
	InternalURL     string
	CallbackBaseURL string
	HostDSN         string
	InternalDSN     string
	Network         string
}

type Option func(*configBuilder)

type configBuilder struct {
	claimProducers  map[string]producerCfg
	executors       map[string]executorCfg
	publishers      map[string]publisherCfg
	namedLocks      map[string]int
	hostAccessPorts []int
	existingNetwork string
	rimskyAlias     string
	blob            *blobCfg
	extraEnv        map[string]string
	sqlite          bool
	bundledFS       *bundledFSCfg
	peerAuthMTLS    bool
}

type bundledFSCfg struct {
	hostDir    string
	configYAML string
}

const sqliteStatePath = "/var/lib/rimsky/state.db"

type blobCfg struct {
	backend                    string
	spillThresholdBytes        int
	orphanSweepInterval        time.Duration
	retentionAfterUnreferenced time.Duration
}

type producerCfg struct {
	endpoint              string
	writeSemanticsAllowed []string
	extraProtocols        []string
}

type executorCfg struct {
	endpoint       string
	transport      string
	extraProtocols []string
}

type publisherCfg struct {
	endpoint string
}

func WithClaimProducer(name, endpoint string, writeSemanticsAllowed ...string) Option {
	return func(cb *configBuilder) {
		if len(writeSemanticsAllowed) == 0 {
			writeSemanticsAllowed = []string{"sync"}
		}
		cb.claimProducers[name] = producerCfg{
			endpoint:              endpoint,
			writeSemanticsAllowed: writeSemanticsAllowed,
		}
	}
}

func WithClaimProducerProtocols(name string, extraProtocols ...string) Option {
	return func(cb *configBuilder) {
		entry, ok := cb.claimProducers[name]
		if !ok {
			panic(fmt.Sprintf("harness.WithClaimProducerProtocols: no claim-producer registered as %q — call WithClaimProducer first", name))
		}
		entry.extraProtocols = append(entry.extraProtocols, extraProtocols...)
		cb.claimProducers[name] = entry
	}
}

func WithExecutor(name, endpoint string) Option {
	return func(cb *configBuilder) {
		cb.executors[name] = executorCfg{endpoint: endpoint, transport: "grpc"}
	}
}

func WithExecutorProtocols(name string, extraProtocols ...string) Option {
	return func(cb *configBuilder) {
		entry, ok := cb.executors[name]
		if !ok {
			panic(fmt.Sprintf("harness.WithExecutorProtocols: no executor registered as %q — call WithExecutor first", name))
		}
		entry.extraProtocols = append(entry.extraProtocols, extraProtocols...)
		cb.executors[name] = entry
	}
}

func WithPublisher(name, endpoint string) Option {
	return func(cb *configBuilder) {
		cb.publishers[name] = publisherCfg{endpoint: endpoint}
	}
}

func WithNamedLock(name string, limit int) Option {
	return func(cb *configBuilder) {
		cb.namedLocks[name] = limit
	}
}

func WithSQLite() Option {
	return func(cb *configBuilder) {
		cb.sqlite = true
	}
}

func WithBlobConfig(backend string, spillThresholdBytes int, orphanSweepInterval, retentionAfterUnreferenced time.Duration) Option {
	return func(cb *configBuilder) {
		cb.blob = &blobCfg{
			backend:                    backend,
			spillThresholdBytes:        spillThresholdBytes,
			orphanSweepInterval:        orphanSweepInterval,
			retentionAfterUnreferenced: retentionAfterUnreferenced,
		}
	}
}

func WithContainerEnv(key, value string) Option {
	return func(cb *configBuilder) {
		if cb.extraEnv == nil {
			cb.extraEnv = map[string]string{}
		}
		cb.extraEnv[key] = value
	}
}

func WithHostPortAccess(ports ...int) Option {
	return func(cb *configBuilder) {
		cb.hostAccessPorts = append(cb.hostAccessPorts, ports...)
	}
}

func WithBundledFilesystemClaimProducer(hostDir, configYAML string) Option {
	return func(cb *configBuilder) {
		cb.bundledFS = &bundledFSCfg{hostDir: hostDir, configYAML: configYAML}
	}
}

// @concept: peer-auth
func WithPeerAuthMTLS(caEncryptionKeyBase64 string) Option {
	return func(cb *configBuilder) {
		cb.peerAuthMTLS = true
		if cb.extraEnv == nil {
			cb.extraEnv = map[string]string{}
		}
		cb.extraEnv["RIMSKY_CA_ENCRYPTION_KEY"] = caEncryptionKeyBase64
	}
}

func SharedNetworkName(ctx context.Context, t testing.TB) string {
	t.Helper()
	name, err := sharedNetworkName(ctx)
	if err != nil {
		t.Fatalf("harness: create network: %v", err)
	}
	return name
}

func NextRimskyAlias() string {
	return fmt.Sprintf("rimsky-%d", nextAliasSuffix())
}

func WithExistingNetwork(name string) Option {
	return func(cb *configBuilder) {
		cb.existingNetwork = name
	}
}

func WithRimskyAlias(alias string) Option {
	return func(cb *configBuilder) {
		cb.rimskyAlias = alias
	}
}

func BringUpRimsky(ctx context.Context, t testing.TB, opts ...Option) RimskyEndpoint {
	t.Helper()
	return BringUpRimskyHandle(ctx, t, opts...).Endpoint
}

type RimskyHandle struct {
	Endpoint RimskyEndpoint

	container testcontainers.Container

	cb        *configBuilder
	yamlBytes []byte

	parentT     testing.TB
	rimskyAlias string
}

func (h *RimskyHandle) DumpRimskyLogs(t testing.TB) {
	t.Helper()
	if h.container == nil {
		return
	}
	dumpLogsForFailure(t, "rimsky/all", h.container)
}

func (h *RimskyHandle) TopProcesses(ctx context.Context, t testing.TB) [][]string {
	t.Helper()
	if h.container == nil {
		t.Fatalf("harness: TopProcesses: no live rimsky container")
	}
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		t.Fatalf("harness: TopProcesses: docker client: %v", err)
	}
	defer cli.Close()
	res, err := cli.ContainerTop(ctx, h.container.GetContainerID(), mobyclient.ContainerTopOptions{})
	if err != nil {
		t.Fatalf("harness: TopProcesses: ContainerTop: %v", err)
	}
	return res.Processes
}

func (h *RimskyHandle) ReadLogs(ctx context.Context, t testing.TB) string {
	t.Helper()
	if h.container == nil {
		t.Fatalf("harness: ReadLogs: no live rimsky container")
	}
	rc, err := h.container.Logs(ctx)
	if err != nil {
		t.Fatalf("harness: ReadLogs: %v", err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("harness: ReadLogs: read: %v", err)
	}
	return string(out)
}

func (h *RimskyHandle) Restart(ctx context.Context, t testing.TB) {
	t.Helper()
	if h.container != nil {
		termCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_ = h.container.Terminate(termCtx)
		cancel()
	}
	cleanupT := h.parentT
	if cleanupT == nil {
		cleanupT = t
	}
	c, baseURL, callbackBaseURL := runRimskyContainerWithCleanupT(ctx, t, cleanupT, h.cb, h.yamlBytes, h.Endpoint.Network, h.rimskyAlias)
	h.container = c
	h.Endpoint.BaseURL = baseURL
	h.Endpoint.CallbackBaseURL = callbackBaseURL
}

func BringUpRimskyHandle(ctx context.Context, t testing.TB, opts ...Option) *RimskyHandle {
	t.Helper()

	cb := &configBuilder{
		claimProducers:  map[string]producerCfg{},
		executors:       map[string]executorCfg{},
		publishers:      map[string]publisherCfg{},
		namedLocks:      map[string]int{},
		hostAccessPorts: nil,
	}
	for _, opt := range opts {
		opt(cb)
	}

	var networkName string
	if cb.existingNetwork != "" {
		networkName = cb.existingNetwork
	} else {
		name, err := sharedNetworkName(ctx)
		if err != nil {
			t.Fatalf("harness: create network: %v", err)
		}
		networkName = name
	}

	rimskyAlias := cb.rimskyAlias
	if rimskyAlias == "" {
		rimskyAlias = NextRimskyAlias()
	}
	pgAlias := rimskyAlias + "-pg"

	var (
		hostDSN     string
		internalDSN string
		yamlBytes   []byte
	)
	if cb.sqlite {
		yamlBytes = []byte(renderRimskyYAMLSQLite(cb))
	} else {
		hostDSN, internalDSN = startPostgresOnNetwork(ctx, t, networkName, pgAlias)

		yamlBytes = []byte(renderRimskyYAML(internalDSN, cb))
	}

	rimsky, baseURL, callbackBaseURL := runRimskyContainer(ctx, t, cb, yamlBytes, networkName, rimskyAlias)

	internalURL := fmt.Sprintf("http://%s:8080", rimskyAlias)

	return &RimskyHandle{
		Endpoint: RimskyEndpoint{
			BaseURL:         baseURL,
			InternalURL:     internalURL,
			CallbackBaseURL: callbackBaseURL,
			HostDSN:         hostDSN,
			InternalDSN:     internalDSN,
			Network:         networkName,
		},
		container:   rimsky,
		cb:          cb,
		yamlBytes:   yamlBytes,
		parentT:     t,
		rimskyAlias: rimskyAlias,
	}
}

func startPostgresOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) (hostDSN, internalDSN string) {
	t.Helper()
	pgContainer, err := runPostgresWithRetry(ctx,
		"postgres:15-alpine",
		pgmodule.WithDatabase("rimsky"),
		pgmodule.WithUsername("rimsky"),
		pgmodule.WithPassword("rimsky"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(120*time.Second),
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(120*time.Second),
			),
		),
		tcnet.WithNetworkName([]string{alias}, networkName),
	)
	if err != nil {
		t.Fatalf("harness: start postgres: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pgContainer.Terminate(termCtx)
	})
	hostDSN, err = pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("harness: postgres host DSN: %v", err)
	}
	internalDSN = fmt.Sprintf("postgres://rimsky:rimsky@%s:5432/rimsky?sslmode=disable", alias)
	return hostDSN, internalDSN
}

func runRimskyContainer(ctx context.Context, t testing.TB, cb *configBuilder, yamlBytes []byte, networkName, alias string) (testcontainers.Container, string, string) {
	return runRimskyContainerWithCleanupT(ctx, t, t, cb, yamlBytes, networkName, alias)
}

func runRimskyContainerWithCleanupT(ctx context.Context, t testing.TB, cleanupT testing.TB, cb *configBuilder, yamlBytes []byte, networkName, alias string) (testcontainers.Container, string, string) {
	t.Helper()
	env := map[string]string{
		"RIMSKY_CONFIG":                             "/etc/rimsky/rimsky.yml",
		"RIMSKY_SUPERVISOR_CONFIG":                  "/etc/rimsky/supervisor-config.yml",
		"RIMSKY_CONTROL_API_HOST":                   "0.0.0.0",
		"RIMSKY_CONTROL_API_PORT":                   "8080",
		"RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST": alias,
		"RIMSKY_OBSERVABILITY_REFRESH_INTERVAL":     "5s",
	}
	if cb.bundledFS != nil {
		env["RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG"] = "/etc/rimsky/claim-producer-filesystem.yml"
	}
	for k, v := range cb.extraEnv {
		env[k] = v
	}
	rimskyOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts("8080/tcp", "9100/tcp"),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(string(yamlBytes)),
			ContainerFilePath: "/etc/rimsky/rimsky.yml",
			FileMode:          0o644,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("8080/tcp").WithStartupTimeout(120 * time.Second),
		),
	}
	if cb.bundledFS != nil {
		rimskyOpts = append(rimskyOpts,
			testcontainers.WithFiles(testcontainers.ContainerFile{
				Reader:            strings.NewReader(cb.bundledFS.configYAML),
				ContainerFilePath: "/etc/rimsky/claim-producer-filesystem.yml",
				FileMode:          0o644,
			}),
			testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
				hc.Binds = append(hc.Binds, cb.bundledFS.hostDir+":/workspace:rw,delegated")
			}),
		)
	}
	if len(cb.hostAccessPorts) > 0 {
		rimskyOpts = append(rimskyOpts, testcontainers.WithHostPortAccess(cb.hostAccessPorts...))
	}
	rimsky, err := runWithRetry(ctx, ImageRef(rimskyAllImage), rimskyOpts...)
	if err != nil {
		t.Fatalf("harness: start rimsky/all: %v", err)
	}
	cleanupT.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = rimsky.Terminate(termCtx)
	})

	hostIP, err := rimsky.Host(ctx)
	if err != nil {
		dumpLogsForFailure(t, "rimsky/all", rimsky)
		t.Fatalf("harness: rimsky host: %v", err)
	}
	mapped, err := rimsky.MappedPort(ctx, "8080")
	if err != nil {
		dumpLogsForFailure(t, "rimsky/all", rimsky)
		t.Fatalf("harness: rimsky mapped port: %v", err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", hostIP, mapped.Port())

	mappedCb, err := rimsky.MappedPort(ctx, "9100")
	if err != nil {
		dumpLogsForFailure(t, "rimsky/all", rimsky)
		t.Fatalf("harness: rimsky callback mapped port: %v", err)
	}
	callbackBaseURL := fmt.Sprintf("http://%s:%s", hostIP, mappedCb.Port())

	if err := waitForHealth(ctx, baseURL); err != nil {
		dumpLogsForFailure(t, "rimsky/all", rimsky)
		t.Fatalf("harness: rimsky /health did not return 200: %v", err)
	}
	return rimsky, baseURL, callbackBaseURL
}

func (e RimskyEndpoint) PostJSON(t testing.TB, path string, body any) (int, []byte) {
	t.Helper()
	return e.PostJSONWithHeaders(t, path, body, nil)
}

func (e RimskyEndpoint) PostJSONWithHeaders(t testing.TB, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("harness: marshal POST %s: %v", path, err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, e.BaseURL+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("harness: build POST %s: %v", path, err)
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		if k == "" || v == "" {
			t.Fatalf("harness: POST %s: header with empty key or value (key=%q, value=%q)", path, k, v)
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("harness: POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// @decision: test-harness-create-instance-wakes-roots-after-create
// @decision: compose-driver-sends-empty-message-after-create
// @story: instance-create-is-idle
func (e RimskyEndpoint) EmptyWakeAfterCreate(t testing.TB, instanceID, idempotencyKeyPrefix, instanceKey string) {
	t.Helper()
	wakeStatus, wakeRaw := e.PostJSONWithHeaders(t,
		"/v1/instances/"+instanceID+"/messages",
		map[string]any{"type": ""},
		map[string]string{"Idempotency-Key": idempotencyKeyPrefix + "-wake-" + instanceKey})
	if wakeStatus != http.StatusCreated && wakeStatus != http.StatusOK {
		t.Fatalf("POST /v1/instances/%s/messages (empty wake): %d %s",
			instanceID, wakeStatus, string(wakeRaw))
	}
}

func (e RimskyEndpoint) DeployTemplate(t testing.TB, body map[string]any) string {
	t.Helper()
	status, raw := e.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := e.PostJSON(t, "/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func (e RimskyEndpoint) CreateInstance(t testing.TB, templateID, instanceKey, wakeIdempotencyKeyPrefix string) string {
	t.Helper()
	status, raw := e.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
		"target_agent": "scenario-default-agent",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	e.EmptyWakeAfterCreate(t, resp.InstanceID, wakeIdempotencyKeyPrefix, instanceKey)
	return resp.InstanceID
}

func (e RimskyEndpoint) GetJSON(t testing.TB, path, bearer string) (int, []byte) {
	t.Helper()
	status, out, err := e.TryGetJSON(path, bearer)
	if err != nil {
		t.Fatalf("harness: GET %s: %v", path, err)
	}
	return status, out
}

func (e RimskyEndpoint) TryGetJSON(path, bearer string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, e.BaseURL+path, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build GET %s: %w", path, err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, nil
}

// @decision: testing-scenario-based-e2e
func (e RimskyEndpoint) WaitForSubscriptionsActive(t testing.TB, instanceID string) {
	t.Helper()
	var last string
	for poll := 1; ; poll++ {
		status, raw, err := e.TryGetJSON("/v1/instances/"+instanceID, "")
		if err != nil {
			last = fmt.Sprintf("GET /v1/instances/%s transport error: %v", instanceID, err)
		} else if status == http.StatusOK {
			var resp struct {
				Subscriptions []struct {
					ID            string `json:"id"`
					PublisherName string `json:"publisher_name"`
					State         string `json:"state"`
					FailureReason string `json:"failure_reason"`
				} `json:"subscriptions"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatalf("harness: decode GET /v1/instances/%s: %v: %s", instanceID, err, string(raw))
			}
			allActive := len(resp.Subscriptions) > 0
			states := make([]string, 0, len(resp.Subscriptions))
			for _, s := range resp.Subscriptions {
				states = append(states, s.PublisherName+"="+s.State)
				if s.State == "failed" {
					t.Fatalf("harness: subscription %s (publisher %q) on instance %s is "+
						"state=failed (reason: %s) — failed is reserved for non-retryable "+
						"errors, waiting longer cannot recover it",
						s.ID, s.PublisherName, instanceID, s.FailureReason)
				}
				if s.State != "active" {
					allActive = false
				}
			}
			last = strings.Join(states, ", ")
			if allActive {
				return
			}
		} else {
			last = fmt.Sprintf("GET /v1/instances/%s returned %d", instanceID, status)
		}
		if poll%50 == 0 {
			t.Logf("harness: still waiting for all subscriptions on instance %s to reach state=active "+
				"(last observed: %s) — the helper blocks until they do; the suite-level timeout is the only backstop",
				instanceID, last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// @decision: testing-scenario-based-e2e
func waitForHealth(ctx context.Context, baseURL string) error {
	last := "no probe completed"
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("waitForHealth: %s/v1/health never returned 200 before the caller's context ended (expected: HTTP 200; last observed: %s): %w", baseURL, last, ctx.Err())
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/health", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Sprintf("HTTP %d", resp.StatusCode)
		} else {
			last = fmt.Sprintf("transport error: %v", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waitForHealth: %s/v1/health never returned 200 before the caller's context ended (expected: HTTP 200; last observed: %s): %w", baseURL, last, ctx.Err())
		//nolint:testwallclock inter-poll pacing between health probes, not a verdict input — the loop only exits on success or the caller's context
		case <-time.After(healthPollInterval):
		}
	}
}

func dumpLogsForFailure(t testing.TB, label string, c testcontainers.Container) {
	t.Helper()
	rc, err := c.Logs(context.Background())
	if err != nil {
		t.Logf("harness: cannot read %s logs: %v", label, err)
		return
	}
	defer rc.Close()
	out, _ := io.ReadAll(rc)
	t.Logf("=== %s container logs ===\n%s\n=== end logs ===", label, string(out))
}

func renderRimskyYAML(internalDSN string, cb *configBuilder) string {
	var b strings.Builder
	b.WriteString("persistence:\n")
	b.WriteString("  driver: postgres\n")
	b.WriteString("  postgres:\n")
	fmt.Fprintf(&b, "    dsn: %q\n", internalDSN)
	writeBlobBlock(&b, cb)
	writePeerBlocks(&b, cb)
	return b.String()
}

func writeBlobBlock(b *strings.Builder, cb *configBuilder) {
	if cb.blob == nil {
		return
	}
	b.WriteString("  blob:\n")
	fmt.Fprintf(b, "    backend: %s\n", cb.blob.backend)
	if cb.blob.spillThresholdBytes > 0 {
		fmt.Fprintf(b, "    spill_threshold_bytes: %d\n", cb.blob.spillThresholdBytes)
	}
	if cb.blob.orphanSweepInterval > 0 || cb.blob.retentionAfterUnreferenced > 0 {
		b.WriteString("    retention:\n")
		if cb.blob.orphanSweepInterval > 0 {
			fmt.Fprintf(b, "      orphan_sweep_interval: %s\n", cb.blob.orphanSweepInterval)
		}
		if cb.blob.retentionAfterUnreferenced > 0 {
			fmt.Fprintf(b, "      retention_after_unreferenced: %s\n", cb.blob.retentionAfterUnreferenced)
		}
	}
}

func renderRimskyYAMLSQLite(cb *configBuilder) string {
	var b strings.Builder
	b.WriteString("persistence:\n")
	b.WriteString("  driver: sqlite\n")
	b.WriteString("  sqlite:\n")
	fmt.Fprintf(&b, "    path: %q\n", sqliteStatePath)
	writeBlobBlock(&b, cb)
	writePeerBlocks(&b, cb)
	return b.String()
}

func writeQuotedList(b *strings.Builder, first string, rest []string) {
	fmt.Fprintf(b, "[%q", first)
	for _, extra := range rest {
		fmt.Fprintf(b, ", %q", extra)
	}
	b.WriteString("]\n")
}

func writePeerBlocks(b *strings.Builder, cb *configBuilder) {
	if cb.peerAuthMTLS {
		b.WriteString("peer_auth: mtls\n")
	}
	if len(cb.claimProducers) == 0 {
		b.WriteString("claim_producers: {}\n")
	} else {
		b.WriteString("claim_producers:\n")
		for name, p := range cb.claimProducers {
			fmt.Fprintf(b, "  %q:\n", name)
			fmt.Fprintf(b, "    endpoint: %q\n", p.endpoint)
			b.WriteString("    protocols: ")
			writeQuotedList(b, "claim_producer", p.extraProtocols)
			b.WriteString("    write_semantics_allowed: ")
			if len(p.writeSemanticsAllowed) == 0 {
				b.WriteString("[]\n")
			} else {
				writeQuotedList(b, p.writeSemanticsAllowed[0], p.writeSemanticsAllowed[1:])
			}
		}
	}

	if len(cb.namedLocks) == 0 {
		b.WriteString("named_locks: {}\n")
	} else {
		b.WriteString("named_locks:\n")
		for name, limit := range cb.namedLocks {
			fmt.Fprintf(b, "  %q: { limit: %d }\n", name, limit)
		}
	}

	if len(cb.executors) == 0 {
		b.WriteString("executors: {}\n")
	} else {
		b.WriteString("executors:\n")
		for name, e := range cb.executors {
			fmt.Fprintf(b, "  %q:\n", name)
			fmt.Fprintf(b, "    transport: %q\n", e.transport)
			fmt.Fprintf(b, "    endpoint: %q\n", e.endpoint)
			b.WriteString("    tls: off\n")
			b.WriteString("    protocols: ")
			writeQuotedList(b, "executor", e.extraProtocols)
		}
	}

	if len(cb.publishers) > 0 {
		b.WriteString("publishers:\n")
		for name, p := range cb.publishers {
			fmt.Fprintf(b, "  %q:\n", name)
			fmt.Fprintf(b, "    endpoint: %q\n", p.endpoint)
			b.WriteString("    protocols: [\"publisher\"]\n")
		}
	}
}
