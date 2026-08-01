// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const claimProducerPostgresImage = "rimsky-claim-producer-postgres"

type PostgresOnNetwork struct {
	InternalDSN string
	HostDSN     string
}

func StartPostgresOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) PostgresOnNetwork {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())
	c, err := runPostgresWithRetry(ctx,
		"postgres:15-alpine",
		pgmodule.WithDatabase("storedb"),
		pgmodule.WithUsername("store"),
		pgmodule.WithPassword("store"),
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(120*time.Second),
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(120*time.Second),
			),
		),
	)
	if err != nil {
		t.Fatalf("harness: start claim-producer postgres: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	hostDSN, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("harness: claim-producer postgres host DSN: %v", err)
	}
	internalDSN := fmt.Sprintf("postgres://store:store@%s:5432/storedb?sslmode=disable", uniqueAlias)

	return PostgresOnNetwork{
		InternalDSN: internalDSN,
		HostDSN:     hostDSN,
	}
}

type PostgresPickPolicy struct {
	ItemsTable               string
	OnCommit                 string
	OnGiveUp                 string
	VisibilityTimeoutSeconds int
}

type PostgresClaimProducerSpec struct {
	Connection           string
	WriteSemantics       string
	PickPolicies         map[string]PostgresPickPolicy
	EnableExecutor       bool
	SweepIntervalSeconds int
}

func StartPostgresClaimProducer(ctx context.Context, t testing.TB, networkName, alias string, spec PostgresClaimProducerSpec) (endpoint string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())

	configYAML := renderPostgresClaimProducerConfig(spec)

	c, err := runWithRetry(ctx, ImageRef(claimProducerPostgresImage),
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
		testcontainers.WithEnv(map[string]string{
			"RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG": "/etc/store/config.yml",
		}),
		testcontainers.WithExposedPorts("9101/tcp"),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(configYAML),
			ContainerFilePath: "/etc/store/config.yml",
			FileMode:          0o644,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9101/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start claim-producer-postgres: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	return fmt.Sprintf("grpc://%s:9101", uniqueAlias)
}

func renderPostgresClaimProducerConfig(spec PostgresClaimProducerSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "connection: %q\n", spec.Connection)
	ws := spec.WriteSemantics
	if ws == "" {
		ws = "staged_async"
	}
	fmt.Fprintf(&b, "write_semantics: %s\n", ws)
	b.WriteString("host: 0.0.0.0\n")
	b.WriteString("grpc_port: 9101\n")
	b.WriteString("http_port: 9111\n")
	b.WriteString("admin_port: 9121\n")
	if spec.EnableExecutor {
		b.WriteString("enable_executor: true\n")
	}
	sweep := spec.SweepIntervalSeconds
	if sweep == 0 {
		sweep = 30
	}
	fmt.Fprintf(&b, "sweep_interval_seconds: %d\n", sweep)
	if len(spec.PickPolicies) > 0 {
		b.WriteString("pick_policies:\n")
		for sel, pp := range spec.PickPolicies {
			fmt.Fprintf(&b, "  %q:\n", sel)
			fmt.Fprintf(&b, "    items_table: %s\n", pp.ItemsTable)
			if pp.OnCommit != "" {
				fmt.Fprintf(&b, "    on_commit: %s\n", pp.OnCommit)
			}
			if pp.OnGiveUp != "" {
				fmt.Fprintf(&b, "    on_give_up: %s\n", pp.OnGiveUp)
			}
			if pp.VisibilityTimeoutSeconds > 0 {
				fmt.Fprintf(&b, "    visibility_timeout_seconds: %d\n", pp.VisibilityTimeoutSeconds)
			}
		}
	}
	return b.String()
}
