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
	"testing"
	"time"

	mobyclient "github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const rimskyAllImage = "rimsky-all-in-one:latest"

const healthDeadline = 90 * time.Second

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
	blob *blobCfg
	extraEnv map[string]string
	sqlite bool
	refValidationMode string
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
	extraProtocols []string
}

type executorCfg struct {
	endpoint  string
	transport string
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

func WithRefValidationMode(mode string) Option {
	return func(cb *configBuilder) {
		cb.refValidationMode = mode
	}
}

func NewNetwork(ctx context.Context, t testing.TB) string {
	t.Helper()
	nw, err := tcnet.New(ctx)
	if err != nil {
		t.Fatalf("harness: create network: %v", err)
	}
	t.Cleanup(func() {
		_ = nw.Remove(context.Background())
	})
	return nw.Name
}

func WithExistingNetwork(name string) Option {
	return func(cb *configBuilder) {
		cb.existingNetwork = name
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

	parentT testing.TB
}

func (h *RimskyHandle) DumpRimskyLogs(t testing.TB) {
	t.Helper()
	if h.container == nil {
		return
	}
	dumpLogsForFailure(t, h.container)
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
	c, baseURL, callbackBaseURL := runRimskyContainerWithCleanupT(ctx, t, cleanupT, h.cb, h.yamlBytes, h.Endpoint.Network)
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
		nw, err := tcnet.New(ctx)
		if err != nil {
			t.Fatalf("harness: create network: %v", err)
		}
		t.Cleanup(func() {
			_ = nw.Remove(context.Background())
		})
		networkName = nw.Name
	}

	var (
		hostDSN     string
		internalDSN string
		yamlBytes   []byte
	)
	if cb.sqlite {
		yamlBytes = []byte(renderRimskyYAMLSQLite(cb))
	} else {
		hostDSN, internalDSN = startPostgresOnNetwork(ctx, t, networkName)

		yamlBytes = []byte(renderRimskyYAML(internalDSN, cb))
	}

	rimsky, baseURL, callbackBaseURL := runRimskyContainer(ctx, t, cb, yamlBytes, networkName)

	internalURL := "http://rimsky:8080"

	return &RimskyHandle{
		Endpoint: RimskyEndpoint{
			BaseURL:         baseURL,
			InternalURL:     internalURL,
			CallbackBaseURL: callbackBaseURL,
			HostDSN:         hostDSN,
			InternalDSN:     internalDSN,
			Network:         networkName,
		},
		container: rimsky,
		cb:        cb,
		yamlBytes: yamlBytes,
		parentT:   t,
	}
}

func startPostgresOnNetwork(ctx context.Context, t testing.TB, networkName string) (hostDSN, internalDSN string) {
	t.Helper()
	pgContainer, err := pgmodule.Run(ctx,
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
		tcnet.WithNetworkName([]string{"rimsky-pg"}, networkName),
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
	internalDSN = "postgres://rimsky:rimsky@rimsky-pg:5432/rimsky?sslmode=disable"
	return hostDSN, internalDSN
}

func runRimskyContainer(ctx context.Context, t testing.TB, cb *configBuilder, yamlBytes []byte, networkName string) (testcontainers.Container, string, string) {
	return runRimskyContainerWithCleanupT(ctx, t, t, cb, yamlBytes, networkName)
}

func runRimskyContainerWithCleanupT(ctx context.Context, t testing.TB, cleanupT testing.TB, cb *configBuilder, yamlBytes []byte, networkName string) (testcontainers.Container, string, string) {
	t.Helper()
	env := map[string]string{
		"RIMSKY_CONFIG":            "/etc/rimsky/rimsky.yml",
		"RIMSKY_SUPERVISOR_CONFIG": "/etc/rimsky/supervisor-config.yml",
		"RIMSKY_CONTROL_API_HOST":  "0.0.0.0",
		"RIMSKY_CONTROL_API_PORT":  "8080",
		"RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST": "rimsky",
		"RIMSKY_OBSERVABILITY_REFRESH_INTERVAL": "5s",
	}
	for k, v := range cb.extraEnv {
		env[k] = v
	}
	rimskyOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts("8080/tcp", "9100/tcp"),
		tcnet.WithNetworkName([]string{"rimsky"}, networkName),
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
	if len(cb.hostAccessPorts) > 0 {
		rimskyOpts = append(rimskyOpts, testcontainers.WithHostPortAccess(cb.hostAccessPorts...))
	}
	rimsky, err := testcontainers.Run(ctx, rimskyAllImage, rimskyOpts...)
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
		dumpLogsForFailure(t, rimsky)
		t.Fatalf("harness: rimsky host: %v", err)
	}
	mapped, err := rimsky.MappedPort(ctx, "8080")
	if err != nil {
		dumpLogsForFailure(t, rimsky)
		t.Fatalf("harness: rimsky mapped port: %v", err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", hostIP, mapped.Port())

	mappedCb, err := rimsky.MappedPort(ctx, "9100")
	if err != nil {
		dumpLogsForFailure(t, rimsky)
		t.Fatalf("harness: rimsky callback mapped port: %v", err)
	}
	callbackBaseURL := fmt.Sprintf("http://%s:%s", hostIP, mappedCb.Port())

	if err := waitForHealth(ctx, baseURL, healthDeadline); err != nil {
		dumpLogsForFailure(t, rimsky)
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
			continue
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
// @decision: compose-driver-emits-empty-message-after-create
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

func (e RimskyEndpoint) GetJSON(t testing.TB, path, bearer string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.BaseURL+path, nil)
	if err != nil {
		t.Fatalf("harness: build GET %s: %v", path, err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("harness: GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (e RimskyEndpoint) WaitForSubscriptionsActive(t testing.TB, instanceID string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		status, raw := e.GetJSON(t, "/v1/instances/"+instanceID, "")
		if status == http.StatusOK {
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
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("harness: subscriptions on instance %s never all reached state=active within "+
		"%v (last observed: %s) — the mounting reconciler is not converging",
		instanceID, deadline, last)
}

func waitForHealth(ctx context.Context, baseURL string, deadline time.Duration) error {
	pollCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for {
		if pollCtx.Err() != nil {
			return fmt.Errorf("timed out after %v", deadline)
		}
		req, _ := http.NewRequestWithContext(pollCtx, http.MethodGet, baseURL+"/v1/health", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out after %v", deadline)
		case <-time.After(healthPollInterval):
		}
	}
}

func dumpLogsForFailure(t testing.TB, c testcontainers.Container) {
	t.Helper()
	rc, err := c.Logs(context.Background())
	if err != nil {
		t.Logf("harness: cannot read rimsky logs: %v", err)
		return
	}
	defer rc.Close()
	out, _ := io.ReadAll(rc)
	t.Logf("=== rimsky/all container logs ===\n%s\n=== end logs ===", string(out))
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

func writePeerBlocks(b *strings.Builder, cb *configBuilder) {
	if cb.refValidationMode != "" {
		b.WriteString("templates:\n")
		fmt.Fprintf(b, "  ref_validation_mode: %s\n", cb.refValidationMode)
	}
	if len(cb.claimProducers) == 0 {
		b.WriteString("claim_producers: {}\n")
	} else {
		b.WriteString("claim_producers:\n")
		for name, p := range cb.claimProducers {
			fmt.Fprintf(b, "  %s:\n", name)
			fmt.Fprintf(b, "    endpoint: %q\n", p.endpoint)
			b.WriteString("    protocols: [claim_producer")
			for _, extra := range p.extraProtocols {
				b.WriteString(", ")
				b.WriteString(extra)
			}
			b.WriteString("]\n")
			b.WriteString("    write_semantics_allowed: [")
			for i, ws := range p.writeSemanticsAllowed {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(ws)
			}
			b.WriteString("]\n")
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
			fmt.Fprintf(b, "  %s:\n", name)
			fmt.Fprintf(b, "    transport: %s\n", e.transport)
			fmt.Fprintf(b, "    endpoint: %q\n", e.endpoint)
			b.WriteString("    tls: off\n")
			b.WriteString("    protocols: [executor")
			for _, extra := range e.extraProtocols {
				b.WriteString(", ")
				b.WriteString(extra)
			}
			b.WriteString("]\n")
		}
	}

	if len(cb.publishers) > 0 {
		b.WriteString("publishers:\n")
		for name, p := range cb.publishers {
			fmt.Fprintf(b, "  %s:\n", name)
			fmt.Fprintf(b, "    endpoint: %q\n", p.endpoint)
			b.WriteString("    protocols: [publisher]\n")
		}
	}
}
