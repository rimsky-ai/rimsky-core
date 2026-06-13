// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// rimsky_split.go is the harness's split-topology boot mode: instead of
// the single rimsky-all-in-one container (rimsky.go), it stands up the
// three-container production shape from the locally-built `rimsky:latest`
// image — one container per role, each launched through the image's
// rimsky-entrypoint with an explicit role command
// (`[rimsky-scheduler]` / `[rimsky-supervisor]` / `[rimsky-control-api]`)
// — against a shared Postgres testcontainer.
//
// Migrate-once rule: a single-role entrypoint invocation runs migrations
// only when the role is rimsky-control-api, so the harness boots the
// control-api container FIRST and waits for /health 200 before starting
// the scheduler and supervisor — by the time those roles open the store
// the schema exists, and exactly one migrate ran (never three racing
// runs, never zero).
package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// rimskyCoreImage is the locally-built role-binaries image (all role
// binaries + rimsky-entrypoint under one PID-1), produced by
// `make core-images`. The split topology runs three containers from
// this one image, differentiated only by the container command.
const rimskyCoreImage = "rimsky:latest"

// splitSupervisorAlias is the supervisor container's in-network
// hostname. It is also the callback advertise host rendered into the
// supervisor's tuning YAML so executors on the shared network can dial
// the async-callback listener back.
const splitSupervisorAlias = "rimsky-supervisor"

// BringUpRimskySplit stands up the three-container split topology:
// a Postgres testcontainer plus three `rimsky:latest` containers
// (control-api, scheduler, supervisor), all on one docker network with
// a shared rendered rimsky.yml. Returns once the control-api serves
// /health 200 AND the scheduler and supervisor report started. The
// returned RimskyEndpoint's BaseURL/InternalURL point at the
// control-api container (in-network alias `rimsky`, same as the
// all-in-one mode, so peer wiring helpers work unchanged).
//
// Options compose exactly like BringUpRimsky's, with two exceptions
// that are t.Fatal'd loudly rather than silently mis-deployed:
// WithSQLite (a split topology cannot share one in-container SQLite
// file across three containers) and a WithBlobConfig naming the
// "memory" backend (gated by rimsky to the single-process mode; the
// roles would refuse to start).
func BringUpRimskySplit(ctx context.Context, t testing.TB, opts ...Option) RimskyEndpoint {
	t.Helper()

	cb := &configBuilder{
		claimProducers: map[string]producerCfg{},
		executors:      map[string]executorCfg{},
		publishers:     map[string]publisherCfg{},
		namedLocks:     map[string]int{},
	}
	for _, opt := range opts {
		opt(cb)
	}
	if cb.sqlite {
		t.Fatalf("harness: BringUpRimskySplit: WithSQLite is not a split-topology option — three containers cannot share one in-container SQLite file; use Postgres (the default)")
	}
	if cb.blob != nil && cb.blob.backend == "memory" {
		t.Fatalf("harness: BringUpRimskySplit: the memory blob backend requires the single-process all-in-one mode (RIMSKY_PROCESS_ROLE=unified); per-role containers would refuse to start")
	}
	if len(cb.hostAccessPorts) > 0 {
		t.Fatalf("harness: BringUpRimskySplit: WithHostPortAccess is not wired for the split mode (it would need per-container tunnels); start the peer as a network sibling instead")
	}

	// 1. Shared docker network (reuse an existing one so sibling peers
	//    can come up first, exactly like the all-in-one mode).
	networkName := cb.existingNetwork
	if networkName == "" {
		nw, err := tcnet.New(ctx)
		if err != nil {
			t.Fatalf("harness: create network: %v", err)
		}
		t.Cleanup(func() {
			_ = nw.Remove(context.Background())
		})
		networkName = nw.Name
	}

	// 2. Shared Postgres.
	hostDSN, internalDSN := startPostgresOnNetwork(ctx, t, networkName)

	// 3. One rendered rimsky.yml shared verbatim by all three role
	//    containers, plus the supervisor's tuning YAML.
	yamlBytes := []byte(renderRimskyYAML(internalDSN, cb))
	supervisorYAML := []byte(renderSplitSupervisorYAML())

	// 4. control-api FIRST — it owns the migrate-once step (see the
	//    package preamble); the other roles need the schema it creates.
	baseURL := startSplitControlAPI(ctx, t, cb, yamlBytes, networkName)

	// 5. Scheduler + supervisor against the migrated store.
	startSplitRole(ctx, t, cb, splitRoleSpec{
		role:    "rimsky-scheduler",
		alias:   "rimsky-scheduler",
		yaml:    yamlBytes,
		network: networkName,
		waitFor: wait.ForLog("scheduler started").WithStartupTimeout(120 * time.Second),
	})
	startSplitRole(ctx, t, cb, splitRoleSpec{
		role:           "rimsky-supervisor",
		alias:          splitSupervisorAlias,
		yaml:           yamlBytes,
		supervisorYAML: supervisorYAML,
		network:        networkName,
		waitFor:        wait.ForLog("supervisor started").WithStartupTimeout(120 * time.Second),
	})

	return RimskyEndpoint{
		BaseURL:     baseURL,
		InternalURL: "http://rimsky:8080",
		HostDSN:     hostDSN,
		InternalDSN: internalDSN,
		Network:     networkName,
	}
}

// splitRoleSpec describes one non-control-api role container.
type splitRoleSpec struct {
	role           string // entrypoint command argument (also the role binary name)
	alias          string // in-network hostname
	yaml           []byte // rendered rimsky.yml (shared across the topology)
	supervisorYAML []byte // rendered supervisor tuning YAML; nil for non-supervisor roles
	network        string
	waitFor        wait.Strategy
}

// startSplitControlAPI boots the control-api role container (command
// `[rimsky-control-api]`), which migrates the shared store before
// serving. Waits for /health 200 on the mapped host port and returns
// the host-reachable base URL.
func startSplitControlAPI(ctx context.Context, t testing.TB, cb *configBuilder, yamlBytes []byte, networkName string) string {
	t.Helper()
	env := map[string]string{
		"RIMSKY_CONFIG":           "/etc/rimsky/rimsky.yml",
		"RIMSKY_CONTROL_API_HOST": "0.0.0.0",
		"RIMSKY_CONTROL_API_PORT": "8080",
	}
	for k, v := range cb.extraEnv {
		env[k] = v
	}
	c, err := testcontainers.Run(ctx, rimskyCoreImage,
		testcontainers.WithCmd("rimsky-control-api"),
		testcontainers.WithExposedPorts("8080/tcp"),
		tcnet.WithNetworkName([]string{"rimsky"}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(string(yamlBytes)),
			ContainerFilePath: "/etc/rimsky/rimsky.yml",
			FileMode:          0o644,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("8080/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start split control-api: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	hostIP, err := c.Host(ctx)
	if err != nil {
		dumpLogsForFailure(t, c)
		t.Fatalf("harness: split control-api host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "8080")
	if err != nil {
		dumpLogsForFailure(t, c)
		t.Fatalf("harness: split control-api mapped port: %v", err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", hostIP, mapped.Port())
	if err := waitForHealth(ctx, baseURL, healthDeadline); err != nil {
		dumpLogsForFailure(t, c)
		t.Fatalf("harness: split control-api /health did not return 200: %v", err)
	}
	return baseURL
}

// startSplitRole boots one scheduler/supervisor role container and
// blocks on its startup log line. Failure dumps the container logs.
func startSplitRole(ctx context.Context, t testing.TB, cb *configBuilder, spec splitRoleSpec) {
	t.Helper()
	env := map[string]string{
		"RIMSKY_CONFIG": "/etc/rimsky/rimsky.yml",
	}
	files := []testcontainers.ContainerFile{{
		Reader:            strings.NewReader(string(spec.yaml)),
		ContainerFilePath: "/etc/rimsky/rimsky.yml",
		FileMode:          0o644,
	}}
	if spec.supervisorYAML != nil {
		env["RIMSKY_SUPERVISOR_CONFIG"] = "/etc/rimsky/supervisor-config.yml"
		env["RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST"] = splitSupervisorAlias
		files = append(files, testcontainers.ContainerFile{
			Reader:            strings.NewReader(string(spec.supervisorYAML)),
			ContainerFilePath: "/etc/rimsky/supervisor-config.yml",
			FileMode:          0o644,
		})
	}
	for k, v := range cb.extraEnv {
		env[k] = v
	}
	c, err := testcontainers.Run(ctx, rimskyCoreImage,
		testcontainers.WithCmd(spec.role),
		tcnet.WithNetworkName([]string{spec.alias}, spec.network),
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(files...),
		testcontainers.WithWaitStrategy(spec.waitFor),
	)
	if err != nil {
		if c != nil {
			dumpLogsForFailure(t, c)
		}
		t.Fatalf("harness: start split %s: %v", spec.role, err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
}

// renderSplitSupervisorYAML builds the supervisor tuning YAML for the
// split topology. Mirrors dockerfiles/all-in-one.supervisor-config.yml's
// shape, but advertises the supervisor container's in-network alias so
// executors on the shared network can POST async callbacks back —
// loopback would silently strand async executors in a multi-container
// deployment.
func renderSplitSupervisorYAML() string {
	var b strings.Builder
	b.WriteString("concurrency: 8\n")
	b.WriteString("heartbeat_interval_ms: 5000\n")
	b.WriteString("claim_poll_interval_ms: 1000\n")
	b.WriteString("callback:\n")
	b.WriteString("  host: 0.0.0.0\n")
	b.WriteString("  port: 9100\n")
	fmt.Fprintf(&b, "  advertise_host: %s\n", splitSupervisorAlias)
	return b.String()
}
