// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package harness brings up a rimsky-core stack from the locally-built
// `rimsky-all-in-one:latest` image (produced by `make core-images`) and
// exposes it to consumption-side tests via its public HTTP API.
//
// Rationale: rimsky enforces a `consumption-side-isolation` depguard
// that bars `graph/`, `runtime/`, `control/`, `foundation/`, `internal/`
// imports from `stores/`, `sensors/`, `subscribers/`, `executors/`. The
// in-process `graph/scenario.Start` harness (used by every pre-2026-
// 05-24 scenario / smoke test) is therefore unreachable from this repo.
// Tests here drive rimsky from outside via testcontainers-go.
//
// Bring-up sequence (default, Postgres backend):
//
//  1. Spin up a Postgres testcontainer (postgres:15-alpine).
//  2. Render an `rimsky.yml` that points rimsky at the testcontainer's
//     in-network DSN, with no claim_producers / executors / publishers
//     declared by default (callers add them via `WithClaimProducer` /
//     `WithExecutor` options).
//  3. Spin up the `rimsky-all-in-one:latest` container on the same docker
//     network, mounting the rendered config at `/etc/rimsky/rimsky.yml`.
//     The unified image's entrypoint runs migrations then spawns
//     scheduler + supervisor + control-api.
//  4. Poll `GET /health` on the control-api's mapped host port until it
//     returns 200.
//  5. Register `t.Cleanup` for both containers + the docker network.
//
// SQLite backend (WithSQLite): no Postgres testcontainer is started; the
// rendered config selects `driver: sqlite` at the image's baked,
// nonroot-writable state path, so all three roles run against one SQLite
// file (single-writer, WAL, busy-timeout). This exercises the image's
// out-of-the-box default rather than reconfiguring it to Postgres.
//
// The harness reuses the locally-built `rimsky-all-in-one:latest` image
// produced by `make core-images`; it does not pull from Docker Hub. The
// all-in-one image bakes in its config and runs all rimsky processes under
// one entrypoint; the harness still mounts a rendered `rimsky.yml` to point
// it at the test Postgres (or the baked SQLite default) + declared peers.
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

	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// rimskyAllImage is rimsky-core's locally-built all-in-one runtime image,
// produced by `make core-images`. The harness consumes the local tag rather
// than pulling from a registry.
const rimskyAllImage = "rimsky-all-in-one:latest"

// healthDeadline bounds the wait for control-api to start serving
// `GET /health` 200 after the container reports as running.
const healthDeadline = 90 * time.Second

// healthPollInterval is the polling cadence between `/health` probes.
const healthPollInterval = 500 * time.Millisecond

// RimskyEndpoint is the bring-up handle returned by BringUpRimsky.
//
// BaseURL is reachable from the test process (host-side, mapped port).
// InternalURL is the rimsky service hostname inside the shared docker
// network — peer services (subscribers, stores) brought up on the same
// network use this to dial back.
//
// Postgres is the DSN of rimsky's state DB; both InternalDSN (for peer
// containers on the same network) and HostDSN (for the test process)
// are surfaced. Several tests connect directly with pgx to assert
// against rimsky's tables or seed operator-owned tables. Both DSN fields
// are empty when the stack is brought up on SQLite (WithSQLite) — there
// is no Postgres in that mode.
type RimskyEndpoint struct {
	BaseURL     string // e.g. http://127.0.0.1:32678 — reach from the test process
	InternalURL string // e.g. http://rimsky:8080 — reach from sibling containers on Network
	HostDSN     string // postgres DSN reachable from the test process
	InternalDSN string // postgres DSN reachable from sibling containers (host=rimsky-pg, port=5432)
	Network     string // docker network name; pass to siblings via testcontainers.WithNetwork
}

// Option configures the rimsky.yml rendered for the rimsky/all
// container. Use With* helpers to compose; pass to BringUpRimsky.
type Option func(*configBuilder)

type configBuilder struct {
	claimProducers  map[string]producerCfg
	executors       map[string]executorCfg
	publishers      map[string]publisherCfg
	namedLocks      map[string]int
	hostAccessPorts []int
	existingNetwork string
	// sqlite selects the image's baked SQLite default instead of a
	// Postgres testcontainer. When true BringUpRimsky brings up no
	// Postgres and renders a `driver: sqlite` config pointing at the
	// image's baked, nonroot-writable state path. See WithSQLite.
	sqlite bool
}

// sqliteStatePath is the SQLite state-file path the rimsky-all-in-one
// image bakes in (dockerfiles/all-in-one.rimsky.yml). It lives under the
// image's nonroot-owned VOLUME (`/var/lib/rimsky`, chowned to 65532 in
// dockerfiles/Dockerfile.rimsky), so the three roles can all open it
// read-write without an extra mount.
const sqliteStatePath = "/var/lib/rimsky/state.db"

type producerCfg struct {
	endpoint              string
	writeSemanticsAllowed []string
}

type executorCfg struct {
	endpoint  string
	transport string // grpc | http (default grpc)
}

type publisherCfg struct {
	endpoint string
}

// WithClaimProducer registers a peer claim-producer service. `endpoint`
// is the in-network URL (e.g. "grpc://store-filesystem:9100"); typical
// callers compute this from a sibling-container name.
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

// WithExecutor registers a peer executor service.
func WithExecutor(name, endpoint string) Option {
	return func(cb *configBuilder) {
		cb.executors[name] = executorCfg{endpoint: endpoint, transport: "grpc"}
	}
}

// WithPublisher registers a peer publisher service.
func WithPublisher(name, endpoint string) Option {
	return func(cb *configBuilder) {
		cb.publishers[name] = publisherCfg{endpoint: endpoint}
	}
}

// WithNamedLock declares a named counting semaphore.
func WithNamedLock(name string, limit int) Option {
	return func(cb *configBuilder) {
		cb.namedLocks[name] = limit
	}
}

// WithSQLite drives the rimsky-all-in-one container on its baked SQLite
// default instead of a Postgres testcontainer. No Postgres container is
// started and no postgres DSN is rendered; the three roles (scheduler +
// supervisor + control-api) run against one SQLite file at
// `/var/lib/rimsky/state.db` (single-writer, WAL, busy-timeout) — the
// out-of-the-box `docker run rimsky-all-in-one` path. The returned
// RimskyEndpoint's HostDSN / InternalDSN are empty in this mode (there
// is no Postgres). Peer wiring (WithExecutor / WithClaimProducer /
// WithNamedLock / WithPublisher) and network options still apply.
func WithSQLite() Option {
	return func(cb *configBuilder) {
		cb.sqlite = true
	}
}

// WithHostPortAccess opens reverse SSH tunnels so the rimsky/all
// container can dial back to a service the test process is running on
// loopback. Inside the container the service is reachable at
// `host.testcontainers.internal:<port>`. Use this to wire an
// in-process peer service (e.g. an fs-store launched on a host port)
// to rimsky without building a docker image for it.
func WithHostPortAccess(ports ...int) Option {
	return func(cb *configBuilder) {
		cb.hostAccessPorts = append(cb.hostAccessPorts, ports...)
	}
}

// NewNetwork creates a docker network registered for cleanup on
// t.Cleanup. Used when callers need to start peer services on the
// shared network BEFORE rimsky/all comes up (e.g. claim producers
// that rimsky dials eagerly at startup).
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

// WithExistingNetwork attaches rimsky/all + its postgres testcontainer
// to a network the caller already created (via NewNetwork). Use this
// when peer services must be brought up on the network BEFORE
// rimsky/all (e.g. claim producers that rimsky dials at startup).
func WithExistingNetwork(name string) Option {
	return func(cb *configBuilder) {
		cb.existingNetwork = name
	}
}

// BringUpRimsky stands up a Postgres testcontainer + a rimsky/all
// container against it on a shared docker network. Returns once
// `GET /health` returns 200. Tears everything down on t.Cleanup.
//
// The returned RimskyEndpoint.BaseURL is reachable from the host (the
// test process); RimskyEndpoint.InternalURL is reachable from sibling
// containers brought up on the same Network.
func BringUpRimsky(ctx context.Context, t testing.TB, opts ...Option) RimskyEndpoint {
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

	// 1. Shared docker network for the rimsky container + any
	//    peer-service siblings. Either reuse an existing network (so
	//    sibling claim producers can come up FIRST and be reachable
	//    when rimsky starts) or create a fresh throwaway network.
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

	// 2. Persistence backend. In Postgres mode (default) bring up a
	//    Postgres testcontainer on the shared network and point rimsky at
	//    its in-network DSN. In SQLite mode (WithSQLite) skip Postgres
	//    entirely — the all-in-one image runs all three roles against its
	//    baked, nonroot-writable SQLite file.
	var (
		hostDSN     string
		internalDSN string
		yamlBytes   []byte
	)
	if cb.sqlite {
		yamlBytes = []byte(renderRimskyYAMLSQLite(cb))
	} else {
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
		// In-network DSN; siblings on the same network reach Postgres at hostname `rimsky-pg`.
		internalDSN = "postgres://rimsky:rimsky@rimsky-pg:5432/rimsky?sslmode=disable"

		// 3. Render rimsky.yml referencing the in-network DSN.
		yamlBytes = []byte(renderRimskyYAML(internalDSN, cb))
	}

	// 4. Bring up rimsky/all. The unified entrypoint runs migrations
	//    then spawns scheduler + supervisor + control-api.
	rimskyOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts("8080/tcp"),
		tcnet.WithNetworkName([]string{"rimsky"}, networkName),
		testcontainers.WithEnv(map[string]string{
			"RIMSKY_CONFIG":            "/etc/rimsky/rimsky.yml",
			"RIMSKY_SUPERVISOR_CONFIG": "/etc/rimsky/supervisor-config.yml",
			"RIMSKY_CONTROL_API_HOST":  "0.0.0.0",
			"RIMSKY_CONTROL_API_PORT":  "8080",
			// In a docker-network deployment the supervisor binds 0.0.0.0
			// for its callback listener; executors need a service-
			// reachable hostname to dial back.
			"RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST": "rimsky",
		}),
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
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = rimsky.Terminate(termCtx)
	})

	hostIP, err := rimsky.Host(ctx)
	if err != nil {
		t.Fatalf("harness: rimsky host: %v", err)
	}
	mapped, err := rimsky.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatalf("harness: rimsky mapped port: %v", err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", hostIP, mapped.Port())
	internalURL := "http://rimsky:8080"

	// 5. Poll /health until control-api accepts traffic.
	if err := waitForHealth(ctx, baseURL, healthDeadline); err != nil {
		dumpLogsForFailure(t, rimsky)
		t.Fatalf("harness: rimsky /health did not return 200: %v", err)
	}

	return RimskyEndpoint{
		BaseURL:     baseURL,
		InternalURL: internalURL,
		HostDSN:     hostDSN,
		InternalDSN: internalDSN,
		Network:     networkName,
	}
}

// PostJSON marshals body to JSON and POSTs to e.BaseURL+path. Helper
// for tests that don't want to wire a full SDK client.
func (e RimskyEndpoint) PostJSON(t testing.TB, path string, body any) (int, []byte) {
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("harness: POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// GetJSON GETs e.BaseURL+path with optional Bearer key.
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

// waitForHealth polls baseURL+"/health" until it returns 200 or the
// deadline elapses.
func waitForHealth(ctx context.Context, baseURL string, deadline time.Duration) error {
	pollCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for {
		if pollCtx.Err() != nil {
			return fmt.Errorf("timed out after %v", deadline)
		}
		req, _ := http.NewRequestWithContext(pollCtx, http.MethodGet, baseURL+"/health", nil)
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

// dumpLogsForFailure logs the rimsky/all container's stdout/stderr at
// test-fail time so a failing harness bring-up doesn't require manual
// `docker logs` to diagnose.
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

// renderRimskyYAML builds the rimsky.yml content for the
// `rimsky/all` container. Format mirrors `deploy/rimsky.yml` (the
// reference config in the rimsky repo).
func renderRimskyYAML(internalDSN string, cb *configBuilder) string {
	var b strings.Builder
	b.WriteString("persistence:\n")
	b.WriteString("  driver: postgres\n")
	b.WriteString("  postgres:\n")
	fmt.Fprintf(&b, "    dsn: %q\n", internalDSN)
	writePeerBlocks(&b, cb)
	return b.String()
}

// renderRimskyYAMLSQLite builds the rimsky.yml content for the
// `rimsky-all-in-one` container's baked SQLite default. It mirrors
// dockerfiles/all-in-one.rimsky.yml's persistence block (driver: sqlite,
// sqlite.path under the image's nonroot-writable VOLUME) and then layers
// on the same peer declarations as the Postgres path so the SQLite stack
// can be driven through a real executor/claim-producer orchestration.
func renderRimskyYAMLSQLite(cb *configBuilder) string {
	var b strings.Builder
	b.WriteString("persistence:\n")
	b.WriteString("  driver: sqlite\n")
	b.WriteString("  sqlite:\n")
	fmt.Fprintf(&b, "    path: %q\n", sqliteStatePath)
	writePeerBlocks(&b, cb)
	return b.String()
}

// writePeerBlocks appends the claim_producers / named_locks / executors /
// publishers declarations to b. Shared by the Postgres and SQLite config
// renderers so peer wiring (WithExecutor / WithClaimProducer / ...) is
// identical regardless of the persistence backend.
func writePeerBlocks(b *strings.Builder, cb *configBuilder) {
	if len(cb.claimProducers) == 0 {
		b.WriteString("claim_producers: {}\n")
	} else {
		b.WriteString("claim_producers:\n")
		for name, p := range cb.claimProducers {
			fmt.Fprintf(b, "  %s:\n", name)
			fmt.Fprintf(b, "    endpoint: %q\n", p.endpoint)
			b.WriteString("    protocols: [claim_producer]\n")
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
			b.WriteString("    protocols: [executor]\n")
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
