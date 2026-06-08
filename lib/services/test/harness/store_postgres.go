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
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// storePostgresImage is the locally-built production postgres-store
// image. Built by `make service-images`.
const storePostgresImage = "rimsky-store-postgres:latest"

// PostgresOnNetwork is the bring-up result for a standalone postgres the
// postgres-store binary uses as its OWN substrate (distinct from
// rimsky's state DB). It is attached to a shared docker network under a
// stable alias so the store container can dial it.
//
// The harness deliberately surfaces only the DSNs — it does NOT open a
// pgx pool. Direct SQL access against the store's substrate is the
// scenario test's own white-box opinion (and the `pgx-isolation`
// depguard allows pgx in `lib/services/test/scenarios/**`, not in the
// harness package). The test opens its own pool from HostDSN to seed /
// inspect the store's tables and staging schemas.
type PostgresOnNetwork struct {
	// InternalDSN is the DSN the store container dials from inside the
	// docker network (host = the network alias).
	InternalDSN string
	// HostDSN is the DSN reachable from the test process — the scenario
	// test opens its own pgx pool against this to seed/inspect.
	HostDSN string
}

// StartPostgresOnNetwork brings up a standalone postgres:15-alpine on the
// given docker network under `alias`, and returns both the in-network DSN
// (for a sibling store container to dial) and the host DSN (for the test
// to open its own pool and seed/inspect). No rimsky migrations are
// applied — this postgres is the store's own substrate, independent of
// rimsky's state DB.
//
// Bring this up BEFORE the store container (the store verifies its
// configured items_table exists at startup) and BEFORE BringUpRimsky
// (rimsky eager-dials its declared claim producers at startup, so the
// store — and therefore its substrate — must already be reachable).
//
// Cleanup (container terminate) is registered via t.Cleanup. Fails hard
// on any error — the harness never t.Skip's.
func StartPostgresOnNetwork(ctx context.Context, t testing.TB, networkName, alias string) PostgresOnNetwork {
	t.Helper()
	c, err := pgmodule.Run(ctx,
		"postgres:15-alpine",
		pgmodule.WithDatabase("storedb"),
		pgmodule.WithUsername("store"),
		pgmodule.WithPassword("store"),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(120*time.Second),
				wait.ForListeningPort("5432/tcp").WithStartupTimeout(120*time.Second),
			),
		),
	)
	if err != nil {
		t.Fatalf("harness: start store postgres: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	hostDSN, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("harness: store postgres host DSN: %v", err)
	}
	internalDSN := fmt.Sprintf("postgres://store:store@%s:5432/storedb?sslmode=disable", alias)

	return PostgresOnNetwork{
		InternalDSN: internalDSN,
		HostDSN:     hostDSN,
	}
}

// PostgresStorePickPolicy mirrors the postgres store's
// `cmd/main.go::yamlPickPolicy`. Only the fields a test drives are
// surfaced. OnCommit / OnGiveUp are rendered verbatim as YAML, so a
// flow-map action (e.g. `{pop_and_move: committed}`) must itself be
// valid inline YAML.
type PostgresStorePickPolicy struct {
	ItemsTable               string
	OnCommit                 string
	OnGiveUp                 string
	VisibilityTimeoutSeconds int
}

// PostgresStoreSpec is the in-test config for a peer postgres-store
// container. Mirrors the YAML shape `stores/postgres/cmd/main.go` reads
// from STORE_POSTGRES_CONFIG.
type PostgresStoreSpec struct {
	// Connection is the in-network DSN the store dials (the substrate
	// postgres). Typically PostgresOnNetwork.InternalDSN.
	Connection string
	// WriteSemantics defaults to staged_async when empty.
	WriteSemantics string
	// PickPolicies are keyed by selector and rendered verbatim into the
	// store's YAML. Each policy's items_table must already exist in the
	// substrate at store startup (the store verifies it).
	PickPolicies map[string]PostgresStorePickPolicy
	// EnableExecutor registers the store's Executor (SQL-verifier) role
	// alongside its ClaimProducer on the same gRPC endpoint.
	EnableExecutor bool
	// SweepIntervalSeconds defaults to 30 when zero.
	SweepIntervalSeconds int
}

// StartPostgresStore brings up the production postgres-store image on the
// given docker network under `alias`, reading a config that points at the
// substrate `spec.Connection`. Returns the in-network endpoint a rimsky
// peer dials via harness.WithClaimProducer.
//
// Bring this up BEFORE BringUpRimsky (rimsky eager-dials its declared
// claim producers at startup) and AFTER the substrate postgres + any
// required items_table (the store verifies the items_table at startup).
// Cleanup is registered via t.Cleanup; fails hard on error.
func StartPostgresStore(ctx context.Context, t testing.TB, networkName, alias string, spec PostgresStoreSpec) (endpoint string) {
	t.Helper()

	configYAML := renderPostgresStoreConfig(spec)

	c, err := testcontainers.Run(ctx, storePostgresImage,
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(map[string]string{
			"STORE_POSTGRES_CONFIG": "/etc/store/config.yml",
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
		t.Fatalf("harness: start store-postgres: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})

	return fmt.Sprintf("grpc://%s:9101", alias)
}

// renderPostgresStoreConfig serializes the in-container YAML config the
// postgres-store binary reads from STORE_POSTGRES_CONFIG.
func renderPostgresStoreConfig(spec PostgresStoreSpec) string {
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
