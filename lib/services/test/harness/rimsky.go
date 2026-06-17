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
//     The unified image's entrypoint runs migrations then runs
//     scheduler + supervisor + control-api in a single process.
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

	mobyclient "github.com/moby/moby/client"
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
	BaseURL     string // @constraint: e.g. http://127.0.0.1:32678 — reach from the test process
	InternalURL string // @constraint: e.g. http://rimsky:8080 — reach from sibling containers on Network
	HostDSN     string // @constraint: postgres DSN reachable from the test process
	InternalDSN string // @constraint: postgres DSN reachable from sibling containers (host=rimsky-pg, port=5432)
	Network     string // @constraint: docker network name; pass to siblings via testcontainers.WithNetwork
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
	// blob, when non-nil, renders a `persistence.blob:` block (backend,
	// spill threshold, orphan-sweep retention tuning). See WithBlobConfig.
	blob *blobCfg
	// extraEnv is merged into the rimsky/all container's environment on
	// top of the harness defaults. See WithContainerEnv.
	extraEnv map[string]string
	// sqlite selects the image's baked SQLite default instead of a
	// Postgres testcontainer. When true BringUpRimsky brings up no
	// Postgres and renders a `driver: sqlite` config pointing at the
	// image's baked, nonroot-writable state path. See WithSQLite.
	sqlite bool
	// refValidationMode is the value rendered into the rimsky.yml
	// `templates.ref_validation_mode` field. Empty defaults to the strict
	// "all" mode the all-in-one image bakes in. Tests that POST templates
	// whose nodes reference an executor the harness has not declared (for
	// example a publisher cascade test where the reactor's executor
	// surfaces work_started but does not need to dispatch successfully)
	// set this to "none" so registration is not refused at the
	// reference-validation gate.
	refValidationMode string
}

// sqliteStatePath is the SQLite state-file path the rimsky-all-in-one
// image bakes in (dockerfiles/all-in-one.rimsky.yml). It lives under the
// image's nonroot-owned VOLUME (`/var/lib/rimsky`, chowned to 65532 in
// dockerfiles/Dockerfile.rimsky), so the three roles can all open it
// read-write without an extra mount.
const sqliteStatePath = "/var/lib/rimsky/state.db"

// blobCfg carries the `persistence.blob:` block rendered by
// WithBlobConfig. Durations are rendered in Go duration syntax, which
// the config loader's yaml.v3 time.Duration decoding accepts.
type blobCfg struct {
	backend                    string
	spillThresholdBytes        int
	orphanSweepInterval        time.Duration
	retentionAfterUnreferenced time.Duration
}

type producerCfg struct {
	endpoint              string
	writeSemanticsAllowed []string
	// extraProtocols are mix-in protocol names appended to the
	// rendered `protocols:` list alongside the always-present
	// `claim_producer`. Used by tests that exercise a producer
	// advertising one of the mix-in protocols (`validation`,
	// `data_processing`, `lifecycle_subscriber`) — the harness writes
	// the union into the rimsky.yml so DialPublisherAndValidationRegistries
	// dials the matching client per advertised protocol.
	extraProtocols []string
}

type executorCfg struct {
	endpoint  string
	transport string // @constraint: grpc | http (default grpc)
	// extraProtocols are mix-in protocol names appended to the
	// rendered `protocols:` list alongside the always-present
	// `executor`. Used by tests that exercise an executor advertising
	// the `lifecycle_subscriber` mix-in — DialLifecycleSubscribers
	// walks the executor entries too and dials a LifecycleClient per
	// peer whose protocols include `lifecycle_subscriber`. The
	// executor's startup observability handshake is best-effort, so
	// piggybacking the lifecycle mix-in on an executor entry avoids
	// the eager-blocking Capabilities handshake the claim-producer
	// path runs.
	extraProtocols []string
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

// WithClaimProducerProtocols extends an already-registered claim-producer
// peer's `protocols:` list with mix-in protocols (`validation`,
// `data_processing`, `lifecycle_subscriber`). The peer MUST have been
// registered with WithClaimProducer earlier in the option chain; the
// helper panics on an unknown name so a test that forgets the prior
// WithClaimProducer call fails loudly instead of silently producing a
// rimsky.yml without the mix-in.
//
// Mix-in advertisement requires two things to line up: the peer's
// `protocols:` block in rimsky.yml must list the mix-in (so
// DialPublisherAndValidationRegistries dials the matching client), AND
// the peer's Capabilities response must list the mix-in in its
// `protocols` field AND populate `validation_supported_roles` when the
// mix-in is `validation`. The harness owns the first; the example
// service code owns the second.
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

// WithExecutor registers a peer executor service.
func WithExecutor(name, endpoint string) Option {
	return func(cb *configBuilder) {
		cb.executors[name] = executorCfg{endpoint: endpoint, transport: "grpc"}
	}
}

// WithExecutorProtocols extends an already-registered executor peer's
// `protocols:` list with mix-in protocols (`lifecycle_subscriber`,
// `validation`). The peer MUST have been registered with WithExecutor
// earlier in the option chain; the helper panics on an unknown name so
// a test that forgets the prior WithExecutor call fails loudly instead
// of silently producing a rimsky.yml without the mix-in.
//
// Mix-in advertisement requires two things to line up: the peer's
// `protocols:` block in rimsky.yml must list the mix-in (so
// DialLifecycleSubscribers / DialPublisherAndValidationRegistries dials
// the matching client), AND the peer's observability Capabilities
// response must list the mix-in in its `protocols` field AND populate
// `validation_supported_roles` when the mix-in is `validation`. The
// harness owns the first; the peer's binary owns the second.
//
// Use this in preference to WithClaimProducerProtocols for cross-stack
// lifecycle-subscriber proofs whose in-process peer is exposed via
// WithHostPortAccess — the claim-producer's eager-blocking Capabilities
// handshake at StartScheduler races the reverse-SSH host-port tunnel
// and flakes under load (per claimproducer_custom.go's preamble), but
// the executor entry's observability handshake is best-effort and the
// LifecycleClient dial happens after startup.
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

// WithBlobConfig renders a `persistence.blob:` block into the rimsky.yml
// the harness mounts: backend selection ("inline" | "pg-largeobject" |
// "filesystem" | "memory"), the spill threshold in bytes, and the
// orphan-sweep retention tuning. Zero-valued durations are omitted so the
// loader's defaults (1h sweep / 24h retention) apply. The "memory"
// backend is gated by rimsky to the single-process all-in-one mode
// (RIMSKY_PROCESS_ROLE=unified, set only by the entrypoint's no-command
// path) — a split or single-role deployment configured with "memory"
// refuses to start.
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

// WithContainerEnv sets an extra environment variable on the rimsky/all
// container, merged over the harness defaults (so a test can, e.g., set
// RIMSKY_LOG_LEVEL=debug to make the scheduler's per-sweep debug lines
// visible in the container logs).
func WithContainerEnv(key, value string) Option {
	return func(cb *configBuilder) {
		if cb.extraEnv == nil {
			cb.extraEnv = map[string]string{}
		}
		cb.extraEnv[key] = value
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

// WithRefValidationMode renders `templates.ref_validation_mode: <mode>`
// into the rimsky.yml the harness mounts. The all-in-one image bakes
// the strict "all" mode by default; tests whose templates reference an
// executor the harness has not declared (for instance the publisher
// cascade test, where the reactor's executor surfaces work_started but
// does not need a real dispatch) set this to "none" so registration is
// not refused at the reference-validation gate. Valid values: "all",
// "available", "none" (case-insensitive; unknown values are accepted by
// the harness and rejected by rimsky at startup).
func WithRefValidationMode(mode string) Option {
	return func(cb *configBuilder) {
		cb.refValidationMode = mode
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
	return BringUpRimskyHandle(ctx, t, opts...).Endpoint
}

// RimskyHandle is the richer bring-up handle returned by
// BringUpRimskyHandle. It exposes the embedded RimskyEndpoint (URLs +
// DSN + Network) plus a Restart method that recreates the rimsky/all
// container against the SAME persistence backend (Postgres testcontainer
// or baked SQLite path) and the SAME peer wiring (executors / publishers
// / claim-producers / named-locks / host-port-access). Use Restart to
// exercise startup-time hooks the control-api runs every time it boots
// (e.g. runtime.ResyncPublisherSubscriptions): the publisher-registry,
// the rimsky_publisher_subscriptions table, and the live publisher's
// in-memory state survive across the restart, so the second boot can
// observe ListSubscriptions being called against the same publisher.
//
// Restart preserves the docker network and the persistence container,
// terminates the previous rimsky/all container, brings up a fresh one
// with identical config, waits for /health 200, and updates
// Endpoint.BaseURL to reflect the new mapped host port (the in-network
// alias `http://rimsky:8080` is unchanged).
type RimskyHandle struct {
	// Endpoint carries the URL + DSN + Network the test process uses
	// to talk to rimsky. BaseURL changes on Restart (new mapped port);
	// InternalURL / HostDSN / InternalDSN / Network are stable.
	Endpoint RimskyEndpoint

	// container holds the live rimsky/all testcontainer so Restart
	// can terminate and recreate it. Kept private — callers operate
	// through the methods, not the raw container.
	container testcontainers.Container

	// @constraint: configBuilder + yamlBytes carry the bring-up inputs so Restart
	// can replay them verbatim against a fresh container.
	cb        *configBuilder
	yamlBytes []byte
}

// DumpRimskyLogs writes the rimsky/all container's combined
// stdout/stderr to t.Log under a clear banner. Used by failing
// assertions to surface what the control-api was actually doing at
// the moment of failure — the resync goroutine's `publisher.resync.*`
// log keys are the canonical evidence of whether reconcile ran. Safe
// to call at any point in the test; never fatal.
func (h *RimskyHandle) DumpRimskyLogs(t testing.TB) {
	t.Helper()
	if h.container == nil {
		return
	}
	dumpLogsForFailure(t, h.container)
}

// TopProcesses returns the rimsky/all container's live process table as
// reported by the Docker daemon (`docker top` over the daemon API):
// one entry per process, each entry being the daemon's column values
// with the command line last. The daemon inspects the container's PID
// namespace from the host, so this works against the distroless image
// (which carries no ps/shell to exec). Used by topology proofs to
// assert the single-process all-in-one mode: exactly one
// rimsky-entrypoint process and zero spawned role children.
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

// ReadLogs returns the rimsky/all container's combined stdout/stderr as
// a string. Unlike DumpRimskyLogs (which writes to t.Log for failure
// forensics) this is for assertions over log content — e.g. the
// orphan-blob sweep's per-handle debug lines.
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

// Restart terminates the current rimsky/all container and brings up a
// fresh one with the SAME config + peer wiring against the SAME
// persistence backend (Postgres testcontainer or baked SQLite file).
// Blocks until `GET /health` returns 200 on the new container. Updates
// h.Endpoint.BaseURL with the new mapped host port.
//
// Failure modes are t.Fatal (the harness never t.Skip's). The
// restarted container's cleanup is registered via t.Cleanup so it is
// torn down at test end exactly like the initial bring-up.
func (h *RimskyHandle) Restart(ctx context.Context, t testing.TB) {
	t.Helper()
	if h.container != nil {
		termCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_ = h.container.Terminate(termCtx)
		cancel()
	}
	c, baseURL := runRimskyContainer(ctx, t, h.cb, h.yamlBytes, h.Endpoint.Network)
	h.container = c
	h.Endpoint.BaseURL = baseURL
}

// BringUpRimskyHandle is BringUpRimsky's restart-capable sibling: it
// performs the same bring-up sequence but returns a *RimskyHandle that
// can be Restart()ed to recreate the rimsky/all container against the
// SAME persistence + peer wiring. The persistence container (Postgres
// testcontainer or SQLite VOLUME) is NOT torn down between restarts —
// the durable state survives, which is what restart-recovery tests
// need.
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

	// @constraint: shared docker network for the rimsky container + any
	// peer-service siblings. Either reuse an existing network (so
	// sibling claim producers can come up FIRST and be reachable
	// when rimsky starts) or create a fresh throwaway network.
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

	// @constraint: persistence backend. In Postgres mode (default) bring up a
	// Postgres testcontainer on the shared network and point rimsky at
	// its in-network DSN. In SQLite mode (WithSQLite) skip Postgres
	// entirely — the all-in-one image runs all three roles against its
	// baked, nonroot-writable SQLite file.
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

	// @constraint: bring up rimsky/all. The unified entrypoint runs migrations
	// then runs scheduler + supervisor + control-api in one
	// process. Extracted to runRimskyContainer so RimskyHandle.Restart
	// can re-run only the rimsky/all bring-up step (the persistence
	// container persists).
	rimsky, baseURL := runRimskyContainer(ctx, t, cb, yamlBytes, networkName)

	internalURL := "http://rimsky:8080"

	return &RimskyHandle{
		Endpoint: RimskyEndpoint{
			BaseURL:     baseURL,
			InternalURL: internalURL,
			HostDSN:     hostDSN,
			InternalDSN: internalDSN,
			Network:     networkName,
		},
		container: rimsky,
		cb:        cb,
		yamlBytes: yamlBytes,
	}
}

// startPostgresOnNetwork brings up the harness's standard Postgres
// testcontainer (postgres:15-alpine, db/user/password all "rimsky") on
// the given docker network under the in-network alias `rimsky-pg`.
// Returns the host-reachable DSN and the in-network DSN. Cleanup is
// registered on t.Cleanup. Shared by the all-in-one bring-up and the
// split-topology bring-up (rimsky_split.go).
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
	// @constraint: in-network DSN; siblings on the same network reach Postgres at hostname `rimsky-pg`.
	internalDSN = "postgres://rimsky:rimsky@rimsky-pg:5432/rimsky?sslmode=disable"
	return hostDSN, internalDSN
}

// runRimskyContainer starts a rimsky/all container with the given
// rendered rimsky.yml on the given docker network, waits for /health
// 200, and returns the container plus its host-mapped base URL. Used
// by BringUpRimskyHandle on the initial boot and by RimskyHandle.Restart
// on subsequent boots — the implementations are identical so they share
// this helper.
func runRimskyContainer(ctx context.Context, t testing.TB, cb *configBuilder, yamlBytes []byte, networkName string) (testcontainers.Container, string) {
	t.Helper()
	env := map[string]string{
		"RIMSKY_CONFIG":            "/etc/rimsky/rimsky.yml",
		"RIMSKY_SUPERVISOR_CONFIG": "/etc/rimsky/supervisor-config.yml",
		"RIMSKY_CONTROL_API_HOST":  "0.0.0.0",
		"RIMSKY_CONTROL_API_PORT":  "8080",
		// @constraint: in a docker-network deployment the supervisor binds 0.0.0.0
		// for its callback listener; executors need a service-
		// reachable hostname to dial back.
		"RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST": "rimsky",
	}
	for k, v := range cb.extraEnv {
		env[k] = v
	}
	rimskyOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts("8080/tcp"),
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
	t.Cleanup(func() {
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
		// @deliberate: most likely cause: rimsky-all-in-one exited non-zero between
		// the wait-strategy port-up observation and the mapped-port
		// lookup — typically a peer the startup eager-dial cannot
		// reach. Dump container logs so the failure mode is visible
		// without a manual `docker logs`.
		dumpLogsForFailure(t, rimsky)
		t.Fatalf("harness: rimsky mapped port: %v", err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", hostIP, mapped.Port())

	if err := waitForHealth(ctx, baseURL, healthDeadline); err != nil {
		dumpLogsForFailure(t, rimsky)
		t.Fatalf("harness: rimsky /health did not return 200: %v", err)
	}
	return rimsky, baseURL
}

// PostJSON marshals body to JSON and POSTs to e.BaseURL+path. Helper
// for tests that don't want to wire a full SDK client.
func (e RimskyEndpoint) PostJSON(t testing.TB, path string, body any) (int, []byte) {
	t.Helper()
	return e.PostJSONWithHeaders(t, path, body, nil)
}

// PostJSONWithHeaders is PostJSON plus arbitrary request headers (e.g.
// Authorization, X-Rimsky-Compose-Origin). Empty / nil headers are
// skipped. Used by tests that need to assert auth-gated paths without
// wiring a full SDK client.
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

// EmptyWakeAfterCreate POSTs an empty-message wake to
// `/v1/instances/{instanceID}/messages` so the deployed template's
// structural roots fire. Instance creation is idle by spec
// (story:instance-create-is-idle); the operator-level empty emit opens
// the first frame for any structural-root receiver
// (`subscribes:` empty/absent) on the template. Idempotency-Key is
// `<idempotencyKeyPrefix>-wake-<instanceKey>`. The status check
// accepts 201 (fresh insert) and 200 (idempotent replay); anything
// else fails the test via t.Fatalf.
//
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

// WaitForSubscriptionsActive polls GET /v1/instances/{id} until every
// publisher subscription on the instance-detail response reports
// state=active, or fails on the bounded deadline. Subscription mounting
// is asynchronous (instance-create returns 201 with rows in `mounting`;
// the reconciler drives Subscribe to `active`), so tests that assert
// sensor-side effects MUST wait on this observable state instead of a
// wall-clock budget. A subscription in `failed` is non-retryable by
// contract, so the helper fails fast with the surfaced reason rather
// than burning the deadline.
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

// waitForHealth polls baseURL+"/v1/health" until it returns 200 or the
// deadline elapses.
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
	writeBlobBlock(&b, cb)
	writePeerBlocks(&b, cb)
	return b.String()
}

// writeBlobBlock appends the `blob:` sub-block (still inside the
// `persistence:` mapping — two-space indent) when WithBlobConfig was
// supplied. Shared by the Postgres and SQLite renderers so blob tuning
// is identical regardless of the persistence backend.
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
	writeBlobBlock(&b, cb)
	writePeerBlocks(&b, cb)
	return b.String()
}

// writePeerBlocks appends the claim_producers / named_locks / executors /
// publishers declarations to b. Shared by the Postgres and SQLite config
// renderers so peer wiring (WithExecutor / WithClaimProducer / ...) is
// identical regardless of the persistence backend. When
// cb.refValidationMode is set, a `templates.ref_validation_mode:` field
// is rendered first so the registration-time validator picks up the
// operator-set mode at startup.
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
			// @constraint: `claim_producer` is the primary protocol; mix-in
			// protocols added via WithClaimProducerProtocols follow.
			// Order is significant only for documentation — the
			// validate-protocols loader treats the list as a set.
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
			// @constraint: `executor` is the primary protocol; mix-in protocols
			// added via WithExecutorProtocols follow. Order is
			// significant only for documentation — the protocols
			// loader treats the list as a set.
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
