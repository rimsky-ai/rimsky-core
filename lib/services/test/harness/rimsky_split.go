// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const rimskyCoreImage = "rimsky:latest"

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

	networkName := cb.existingNetwork
	if networkName == "" {
		name, err := sharedNetworkName(ctx)
		if err != nil {
			t.Fatalf("harness: create network: %v", err)
		}
		networkName = name
	}

	suffix := nextAliasSuffix()
	controlAlias := fmt.Sprintf("rimsky-%d", suffix)
	pgAlias := fmt.Sprintf("rimsky-pg-%d", suffix)
	schedulerAlias := fmt.Sprintf("rimsky-scheduler-%d", suffix)
	supervisorAlias := fmt.Sprintf("rimsky-supervisor-%d", suffix)

	hostDSN, internalDSN := startPostgresOnNetwork(ctx, t, networkName, pgAlias)

	yamlBytes := []byte(renderRimskyYAML(internalDSN, cb))
	supervisorYAML := []byte(renderSplitSupervisorYAML(supervisorAlias))

	baseURL := startSplitControlAPI(ctx, t, cb, yamlBytes, networkName, controlAlias)

	startSplitRole(ctx, t, cb, splitRoleSpec{
		role:    "rimsky-scheduler",
		alias:   schedulerAlias,
		yaml:    yamlBytes,
		network: networkName,
		waitFor: wait.ForLog("scheduler started").WithStartupTimeout(120 * time.Second),
	})
	startSplitRole(ctx, t, cb, splitRoleSpec{
		role:           "rimsky-supervisor",
		alias:          supervisorAlias,
		advertiseHost:  supervisorAlias,
		yaml:           yamlBytes,
		supervisorYAML: supervisorYAML,
		network:        networkName,
		waitFor:        wait.ForLog("supervisor started").WithStartupTimeout(120 * time.Second),
	})

	return RimskyEndpoint{
		BaseURL:     baseURL,
		InternalURL: fmt.Sprintf("http://%s:8080", controlAlias),
		HostDSN:     hostDSN,
		InternalDSN: internalDSN,
		Network:     networkName,
	}
}

type splitRoleSpec struct {
	role           string
	alias          string
	advertiseHost  string
	yaml           []byte
	supervisorYAML []byte
	network        string
	waitFor        wait.Strategy
}

func startSplitControlAPI(ctx context.Context, t testing.TB, cb *configBuilder, yamlBytes []byte, networkName, alias string) string {
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
		tcnet.WithNetworkName([]string{alias}, networkName),
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
		env["RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST"] = spec.advertiseHost
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

func renderSplitSupervisorYAML(advertiseHost string) string {
	var b strings.Builder
	b.WriteString("concurrency: 8\n")
	b.WriteString("claim_poll_interval_ms: 200\n")
	b.WriteString("callback:\n")
	b.WriteString("  host: 0.0.0.0\n")
	b.WriteString("  port: 9100\n")
	fmt.Fprintf(&b, "  advertise_host: %s\n", advertiseHost)
	return b.String()
}
