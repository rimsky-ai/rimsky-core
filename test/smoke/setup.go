// Package smoke is the §19.2 acceptance fixture for the stores redesign.
//
// BringUpStack(t) spins up the entire rimsky stack in one process against
// a testcontainers postgres:
//   - migrate the rewritten 001-initial.sql,
//   - create the operator-owned `topics_items` items table (per spec §9.10),
//   - register the real `filesystem` (direct mode) and `claim-store-postgres`
//     factories with a programmatically-built `store.StoresConfig` whose
//     `content` filesystem store is rooted at `t.TempDir()`,
//   - start scheduler / supervisor / control-api in-process via the
//     library entry points in `core/config`,
//   - register the stub claude-agent gRPC server with scripted per-node
//     completions (scope / draft / review) per §19.2 step 5.
//
// The smoke test in `stores_redesign_smoke_test.go` exercises the four-node
// §11.5 worked-example template against this stack: 100 force-fires of the
// claim-topic source node followed by polling for the downstream cascade
// to drain.
//
// Programmatic store config: the smoke fixture cannot use a static
// `stores.yml` because the filesystem-direct `root` must be a per-test
// `t.TempDir()` (parallel-safe and self-cleaning). BringUpStack therefore
// builds a `store.StoresConfig` in Go with the concrete tmpdir path.
package smoke

import (
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

	"github.com/fallguy/rimsky/core/config"
	"github.com/fallguy/rimsky/core/executor"
	"github.com/fallguy/rimsky/core/migrations"
	pgqueue "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/shared"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/filesystem"
	pgstore "github.com/fallguy/rimsky/core/store/postgres"
	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// SmokeStack bundles the live, fully-wired stack. The smoke test interacts
// with rimsky exclusively through ControlBase (HTTP+JSON) and Pool (for the
// raw SQL assertions called out in spec §19.2).
type SmokeStack struct {
	T testing.TB

	Pool *pgxpool.Pool

	// ControlBase is "http://127.0.0.1:<port>" of the in-process control-api.
	ControlBase string

	// ItemsTable is the operator-owned items table for the topics-ring claim
	// store. Created by BringUpStack via direct SQL.
	ItemsTable string

	// ContentRoot is the temp directory backing the `content` filesystem
	// store. Surfaced so the test can inspect / clean up if needed (t.TempDir
	// already auto-cleans).
	ContentRoot string

	// Executor is the in-process stub gRPC server registered as
	// `claude-agent`. The smoke test does not interact with it directly.
	Executor *smokeExecutor
}

// BringUpStack stands up the full rimsky stack per §19.2. Returns a
// SmokeStack handle once every component is reachable; failures are
// fatal via t.Fatalf so callers can dereference Pool / ControlBase
// without checking errors.
//
// Cleanups (postgres teardown, server shutdowns, executor stop) are
// registered with t.Cleanup so the caller does not need a defer.
func BringUpStack(t *testing.T) *SmokeStack {
	t.Helper()
	ctx := context.Background()

	pool, teardown := startPostgresWithMigrations(ctx, t)
	t.Cleanup(teardown)

	const itemsTable = "topics_items"
	createTopicsItemsTable(t, pool, itemsTable)

	contentRoot := t.TempDir()
	storesCfg := buildStoresConfig(itemsTable, contentRoot, pool.Config().ConnString())

	storeFactories := []store.Factory{
		filesystem.Factory{},
		pgstore.Factory{},
	}

	stub := newSmokeExecutor()
	_, stubAddr := stub.listen(t)
	t.Cleanup(stub.stop)

	resolver := executor.NewStaticResolver(map[string]executor.Endpoint{
		"claude-agent": {Transport: "grpc", URL: stubAddr},
	})

	logger := shared.SilentLogger{}
	clock := shared.SystemClock{}

	sb := pgstorage.New(pool)
	q := pgqueue.New(pool)

	// Scheduler — 50ms tick (smoke-test only override per §19.2 step 5).
	sh, err := config.StartScheduler(config.SchedulerConfig{
		Storage:        sb,
		Queue:          q,
		Clock:          clock,
		Logger:         logger,
		TickInterval:   50 * time.Millisecond,
		Pool:           pool,
		StoreFactories: storeFactories,
		Stores:         storesCfg,
	})
	if err != nil {
		t.Fatalf("BringUpStack: StartScheduler: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sh.Shutdown(ctx)
	})

	// Supervisor.
	sv, err := config.StartSupervisor(config.SupervisorConfig{
		SupervisorID:      "smoke-supervisor",
		Storage:           sb,
		Queue:             q,
		Clock:             clock,
		Logger:            logger,
		Concurrency:       8,
		HeartbeatInterval: 500 * time.Millisecond,
		ClaimPollInterval: 100 * time.Millisecond,
		Resolver:          resolver,
		StoreFactories:    storeFactories,
		Stores:            storesCfg,
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

	// Control API.
	ca, err := config.StartControlAPI(config.ControlAPIConfig{
		Storage:        sb,
		Queue:          q,
		Clock:          clock,
		Logger:         logger,
		Host:           "127.0.0.1",
		Port:           0,
		StoreFactories: storeFactories,
		Stores:         storesCfg,
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
		T:           t,
		Pool:        pool,
		ControlBase: "http://" + ca.Addr(),
		ItemsTable:  itemsTable,
		ContentRoot: contentRoot,
		Executor:    stub,
	}
}

// buildStoresConfig constructs the §15.1 stores config in Go. The
// filesystem `content` store points at `contentRoot`; the postgres
// `topics-ring` exposes one pick policy at the `@review-queue`
// selector with ring-buffer defaults. `dsn` is the testcontainers DSN
// the smoke fixture spun up — the smoke deployment collocates the
// workload store with rimsky's control plane on the same database, so
// the same DSN is supplied for both.
//
// Built programmatically rather than from a YAML fixture because the
// `root` path is a per-test `t.TempDir()` and the `dsn` is per-test
// container.
func buildStoresConfig(itemsTable, contentRoot, dsn string) store.StoresConfig {
	return store.StoresConfig{
		Stores: map[string]map[string]any{
			"content": {
				"kind": "filesystem",
				"mode": "direct",
				"root": contentRoot,
			},
			"topics-ring": {
				"kind":            "postgres",
				"connection":      dsn,
				"write_semantics": "direct",
				"pick_policies": map[string]any{
					"@review-queue": map[string]any{
						"type":                       "ring",
						"items_table":                itemsTable,
						"on_commit_default":          "release_to_back",
						"on_give_up_default":         "release_to_back",
						"visibility_timeout_seconds": 300,
					},
				},
			},
		},
	}
}

// createTopicsItemsTable creates the operator-owned items table per
// §12.12. Matches the column shape verified by pgstore.Factory.Build's
// information_schema check.
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

// PostJSON marshals body to JSON and POSTs to controlBase+path. Returns
// status code and response body (as bytes). Failures are fatal via t.Fatalf.
func (s *SmokeStack) PostJSON(path string, body any) (int, []byte) {
	s.T.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			s.T.Fatalf("PostJSON: marshal: %v", err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, s.ControlBase+path, strings.NewReader(string(raw)))
	if err != nil {
		s.T.Fatalf("PostJSON: NewRequest: %v", err)
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.T.Fatalf("PostJSON: Do: %v", err)
	}
	defer resp.Body.Close()
	respRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.T.Fatalf("PostJSON: read body: %v", err)
	}
	return resp.StatusCode, respRaw
}

// ------------------------------------------------------------------
// Stub gRPC executor (mimics the claude-agent and http-node binaries
// running in stub mode per §19.2 step 5).
// ------------------------------------------------------------------

// smokeExecutor is the in-process gRPC NodeExecutor server used for the
// smoke fixture. It returns a scripted Complete{changed:true,
// attributes_delta:<per-node-type fixture>} per spec §19.2 step 5:
//
//	scope  → {"scope_notes": "stub"}
//	draft  → {}    (writes a fixed string to its write region)
//	review → {"accepted": true}
//
// Any other node_type returns Complete{changed:true, attributes_delta:{}}.
//
// The stub does NOT actually write a file in the draft path: §19.2's
// "writes a fixed string to its write region" is illustrative — the
// supervisor's region-lock semantics are exercised via filesystem.Store's
// AcquireLock irrespective of whether bytes land. Adding actual file
// writes would couple the stub to the filesystem store's resolved path,
// which is plumbed through the executor handle. Out of scope for the
// smoke fixture; the existing region-lock scenarios in
// `test/scenarios/stores/` cover the lock side directly.
type smokeExecutor struct {
	genv1.UnimplementedNodeExecutorServer
	srv *grpc.Server
}

func newSmokeExecutor() *smokeExecutor { return &smokeExecutor{} }

// listen binds 127.0.0.1:0, registers the stub as the NodeExecutor, and
// starts the gRPC server in a background goroutine. Returns the server
// and its bound address. Cleanup is the caller's responsibility (smoke
// stack registers stop via t.Cleanup).
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

// stop drives the gRPC server's graceful stop with a short timeout. Used
// by t.Cleanup. Errors are not propagated — test cleanup runs after the
// test result is decided.
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

// Execute returns a single Complete event per request. attributes_delta
// is keyed by node_type via stubAttributesFor.
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

// stubAttributesFor returns the §19.2 step 5 fixture keyed by node_type.
// Unknown node_types get an empty delta — the supervisor treats an empty
// Struct as "no per-field writeback".
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

// AssertUUID parses a string UUID or fails the test. Used by callers that
// pull an ID out of a JSON response.
func (s *SmokeStack) AssertUUID(v string) shared.UUID {
	s.T.Helper()
	id, err := uuid.Parse(v)
	if err != nil {
		s.T.Fatalf("AssertUUID: parse %q: %v", v, err)
	}
	return id
}

// startPostgresWithMigrations spins up a throwaway Postgres 14 container,
// runs the rimsky migrations, and returns a pool plus teardown closure.
//
// @source: core/internal/pgtest/pgtest.go::StartPostgres
// @diverged: true
// @reason: core/internal/pgtest is a Go-internal package and cannot be
// imported from `test/smoke/...`. The smoke fixture is the only out-of-
// core consumer; inlining the ~40 lines is cheaper than relocating the
// helper.
func startPostgresWithMigrations(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
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
		t.Fatalf("startPostgresWithMigrations: container: %v", err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("startPostgresWithMigrations: dsn: %v", err)
	}
	pool, err := waitForPool(ctx, dsn, 30*time.Second)
	if err != nil {
		t.Fatalf("startPostgresWithMigrations: pool: %v", err)
	}
	if err := migrations.Run(ctx, pool, shared.SilentLogger{}); err != nil {
		pool.Close()
		_ = container.Terminate(context.Background())
		t.Fatalf("startPostgresWithMigrations: migrate: %v", err)
	}
	teardown := func() {
		pool.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("startPostgresWithMigrations: terminate warn: %v", err)
		}
	}
	return pool, teardown
}

// waitForPool retries pool construction until the postgres container
// answers a ping or the timeout elapses. Mirrors the
// core/internal/pgtest helper's loop.
func waitForPool(ctx context.Context, dsn string, timeout time.Duration) (*pgxpool.Pool, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err := pool.Ping(ctx); err == nil {
			return pool, nil
		} else {
			pool.Close()
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
	}
	return nil, fmt.Errorf("waitForPool: not ready within %s: %w", timeout, lastErr)
}
