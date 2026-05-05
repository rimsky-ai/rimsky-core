// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package smoke is the acceptance fixture for the stores redesign.
//
// BringUpStack(t) spins up the entire rimsky stack in one process
// against a testcontainers postgres and three loopback store-services
// (filesystem, postgres) bound to ephemeral ports per spec §10.
package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	pgpersist "github.com/fallguy/rimsky/foundation/persistence/postgres"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/executor"
	"github.com/fallguy/rimsky/modeling/shared"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
	fsfixture "github.com/fallguy/rimsky/stores/filesystem/testfixture"
	pgsstore "github.com/fallguy/rimsky/stores/postgres/store"
	pgsfixture "github.com/fallguy/rimsky/stores/postgres/testfixture"
)

// SmokeStack bundles the live, fully-wired stack.
type SmokeStack struct {
	T testing.TB

	// Pool is the test-only escape hatch for raw SQL fixtures.
	// Production code goes through Driver / Persist / Queue.
	Pool   *pgxpool.Pool
	Driver persistence.Driver

	// ControlBase is "http://127.0.0.1:<port>" of the in-process
	// control-api.
	ControlBase string

	// PostgresStoreAdminURL is the store-internal admin endpoint
	// of the postgres store-service. The smoke test POSTs items to
	// `/admin/items/<selector>` here for seeding (per v3 spec §7.3 step 1).
	PostgresStoreAdminURL string

	// ItemsTable is the operator-owned items table for the topics
	// store-service. Created by BringUpStack via direct SQL.
	ItemsTable string

	// ContentRoot is the temp directory backing the `content`
	// filesystem store-service.
	ContentRoot string

	// Executor is the in-process stub gRPC server registered as
	// `claude-agent`.
	Executor *smokeExecutor
}

// BringUpStack stands up the full rimsky stack. Returns a SmokeStack
// handle once every component is reachable.
func BringUpStack(t *testing.T) *SmokeStack {
	t.Helper()
	ctx := context.Background()

	driver, pool, teardown := openDriverWithMigrations(ctx, t)
	t.Cleanup(teardown)

	const itemsTable = "topics_items"
	createTopicsItemsTable(t, pool, itemsTable)

	contentRoot := t.TempDir()

	// Loopback filesystem store-service.
	fsEndpoint, _, fsTeardown := fsfixture.Start(t, fsfixture.Config{Root: contentRoot})
	t.Cleanup(fsTeardown)

	// Loopback postgres store-service. Owns its own pgx pool against
	// the same testcontainers DSN.
	dsn := pool.Config().ConnString()
	pgsEndpoint, pgsAdminEndpoint, pgsTeardown := pgsfixture.Start(t, pgsfixture.Config{
		Connection:     dsn,
		WriteSemantics: locks.WriteSemanticsSync,
		PickPolicies: map[string]*pgsstore.PickPolicy{
			"@review-queue": {
				ItemsTable:        itemsTable,
				OnCommitDefault:   "release_to_back",
				OnGiveUpDefault:   "release_to_back",
				VisibilityTimeout: 300 * time.Second,
			},
		},
		SweepInterval: 10 * time.Second,
		WithAdmin:     true,
	})
	t.Cleanup(pgsTeardown)

	storesCfg := config.RemoteStoresConfig{
		Stores: map[string]config.StoreEntry{
			"content": {
				Endpoint:     "grpc://" + fsEndpoint,
				Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				Protocols:    []string{config.ProtocolClaimProducer},
			},
			"topics-ring": {
				Endpoint:     "grpc://" + pgsEndpoint,
				Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				Protocols:    []string{config.ProtocolClaimProducer},
			},
		},
	}

	// Named locks the smoke template references. The supervisor
	// enforces the per-name limit at acquire time (counter-semaphore
	// semantics under the per-name advisory lock); empty config →
	// templates referencing any name fail validation at deploy.
	namedLocksCfg := locks.NamedLocksConfig{
		Locks: map[string]locks.NamedLockConfig{
			"topics-ring:concurrent-claims": {Limit: 5},
			"model-budget":                  {Limit: 50},
		},
	}

	stub := newSmokeExecutor()
	_, stubAddr := stub.listen(t)
	t.Cleanup(stub.stop)

	resolver := executor.NewStaticResolver(map[string]executor.Endpoint{
		"claude-agent": {Transport: "grpc", URL: stubAddr},
	})

	logger := shared.SilentLogger{}
	clock := shared.SystemClock{}

	sh, err := config.StartScheduler(config.SchedulerConfig{
		Driver:       driver,
		Clock:        clock,
		Logger:       logger,
		TickInterval: 50 * time.Millisecond,
		Stores:       storesCfg,
		NamedLocks:   namedLocksCfg,
	})
	if err != nil {
		t.Fatalf("BringUpStack: StartScheduler: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sh.Shutdown(ctx)
	})

	sv, err := config.StartSupervisor(config.SupervisorConfig{
		SupervisorID:      "smoke-supervisor",
		Driver:            driver,
		Clock:             clock,
		Logger:            logger,
		Concurrency:       8,
		HeartbeatInterval: 500 * time.Millisecond,
		ClaimPollInterval: 100 * time.Millisecond,
		Resolver:          resolver,
		Stores:            storesCfg,
		NamedLocks:        namedLocksCfg,
		CallbackHost:      "127.0.0.1",
		CallbackPort:      0,
	})
	if err != nil {
		t.Fatalf("BringUpStack: StartSupervisor: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sv.Shutdown(ctx)
	})

	ca, err := config.StartControlAPI(config.ControlAPIConfig{
		Driver:     driver,
		Clock:      clock,
		Logger:     logger,
		Host:       "127.0.0.1",
		Port:       0,
		Stores:     storesCfg,
		NamedLocks: namedLocksCfg,
		Executors: config.ExecutorsConfig{
			Executors: map[string]config.ExecutorEntry{
				"claude-agent": {Transport: "grpc", Endpoint: stubAddr, TLS: "off"},
			},
		},
	})
	if err != nil {
		t.Fatalf("BringUpStack: StartControlAPI: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ca.Shutdown(ctx)
	})

	return &SmokeStack{
		T:                     t,
		Pool:                  pool,
		Driver:                driver,
		ControlBase:           "http://" + ca.Addr(),
		PostgresStoreAdminURL: "http://" + pgsAdminEndpoint,
		ItemsTable:            itemsTable,
		ContentRoot:           contentRoot,
		Executor:              stub,
	}
}

// createTopicsItemsTable creates the operator-owned items table.
func createTopicsItemsTable(t *testing.T, pool *pgxpool.Pool, table string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), fmt.Sprintf(`
		CREATE TABLE %s (
			item_id     TEXT PRIMARY KEY,
			payload     JSONB NOT NULL,
			state       TEXT NOT NULL DEFAULT 'available',
			claim_token TEXT,
			claimed_at  TIMESTAMPTZ,
			enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			priority    INTEGER NOT NULL DEFAULT 0,
			sequence    BIGSERIAL
		);
		CREATE INDEX %s_available_idx   ON %s (priority DESC, sequence) WHERE state = 'available';
		CREATE INDEX %s_in_progress_idx ON %s (claim_token) WHERE state = 'in_progress';
	`, table, table, table, table, table))
	if err != nil {
		t.Fatalf("createTopicsItemsTable: %v", err)
	}
}

// PostJSON marshals body to JSON and POSTs to ControlBase+path.
func (s *SmokeStack) PostJSON(path string, body any) (int, []byte) {
	s.T.Helper()
	return postJSON(s.T, s.ControlBase+path, body)
}

// PostStoreAdmin POSTs items to the store's admin endpoint for
// seeding pick-policy items.
func (s *SmokeStack) PostStoreAdmin(path string, body any) (int, []byte) {
	s.T.Helper()
	return postJSON(s.T, s.PostgresStoreAdminURL+path, body)
}

func postJSON(t testing.TB, url string, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("postJSON: marshal: %v", err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("postJSON: NewRequest: %v", err)
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("postJSON: Do: %v", err)
	}
	defer resp.Body.Close()
	respRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("postJSON: read body: %v", err)
	}
	return resp.StatusCode, respRaw
}

// _ guards against unused-import on bytes when this file is read out of
// context.
var _ = bytes.NewReader

// ----------------------------------------------------------------------
// Stub gRPC executor.
// ----------------------------------------------------------------------

type smokeExecutor struct {
	genv1.UnimplementedNodeExecutorServer
	srv *grpc.Server
}

func newSmokeExecutor() *smokeExecutor { return &smokeExecutor{} }

func (s *smokeExecutor) listen(t *testing.T) (*grpc.Server, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("smokeExecutor: listen: %v", err)
	}
	s.srv = grpc.NewServer()
	genv1.RegisterNodeExecutorServer(s.srv, s)
	go func() { _ = s.srv.Serve(lis) }()
	return s.srv, lis.Addr().String()
}

func (s *smokeExecutor) stop() {
	if s.srv == nil {
		return
	}
	done := make(chan struct{})
	go func() { s.srv.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.srv.Stop()
	}
}

func (s *smokeExecutor) Execute(req *genv1.ExecuteRequest, stream genv1.NodeExecutor_ExecuteServer) error {
	delta, err := structpb.NewStruct(stubAttributesFor(req.GetNodeType()))
	if err != nil {
		return err
	}
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{Complete: &genv1.Complete{
		AttributesDelta: delta,
		Changed:         true,
		ChangeSummary:   "smoke-stub",
	}}})
}

func stubAttributesFor(nodeType string) map[string]any {
	switch nodeType {
	case "scope":
		return map[string]any{"scope_notes": "stub"}
	case "draft":
		return map[string]any{}
	case "review":
		return map[string]any{"accepted": true}
	default:
		return map[string]any{}
	}
}

// AssertUUID parses a string UUID or fails the test.
func (s *SmokeStack) AssertUUID(v string) shared.UUID {
	s.T.Helper()
	id, err := uuid.Parse(v)
	if err != nil {
		s.T.Fatalf("AssertUUID: parse %q: %v", v, err)
	}
	return id
}

// openDriverWithMigrations spins up a throwaway Postgres container,
// opens a persistence.Driver against it, applies migrations, and
// extracts the underlying *pgxpool.Pool for raw-SQL fixture seeding.
func openDriverWithMigrations(ctx context.Context, t *testing.T) (persistence.Driver, *pgxpool.Pool, func()) {
	t.Helper()
	container, err := pgmodule.Run(ctx,
		"postgres:14-alpine",
		pgmodule.WithDatabase("rimsky"),
		pgmodule.WithUsername("rimsky"),
		pgmodule.WithPassword("rimsky"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("openDriverWithMigrations: container: %v", err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("openDriverWithMigrations: dsn: %v", err)
	}
	driver, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		_ = container.Terminate(context.Background())
		t.Fatalf("openDriverWithMigrations: open driver: %v", err)
	}
	if err := driver.Migrate(ctx, shared.SilentLogger{}); err != nil {
		_ = driver.Close()
		_ = container.Terminate(context.Background())
		t.Fatalf("openDriverWithMigrations: migrate: %v", err)
	}
	pool, ok := pgpersist.PoolFromDriverForTest(driver)
	if !ok {
		_ = driver.Close()
		_ = container.Terminate(context.Background())
		t.Fatalf("openDriverWithMigrations: PoolFromDriverForTest returned !ok")
	}
	teardown := func() {
		_ = driver.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("openDriverWithMigrations: terminate warn: %v", err)
		}
	}
	return driver, pool, teardown
}
