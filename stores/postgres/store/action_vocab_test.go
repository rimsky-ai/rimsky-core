// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Action-vocabulary tests for the postgres store. The validator
// + dispatch surface tests run without a live pool. The Pop / Recycle
// SQL paths run against a throwaway postgres container (testcontainers)
// — see TestPGAction_Pop_RowDeleted and TestPGAction_Recycle_RowReturnsToQueue.

package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gopkg.in/yaml.v3"

	corestore "github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/stores/common/action"
)

// TestPGMigration_OldFieldNames pins that an operator config using the
// pre-v2 field names (`on_commit_default`) and old action values
// (`release_to_back`, `delete`) is rejected at config-load. The
// pg-store cmd's yamlPickPolicy now uses `on_commit` / `on_give_up`
// (with action.Action types), so the old field names are silently
// dropped by yaml.v3 and the action then validates as zero-Kind.
//
// The validator rejects zero-Kind with the "required (got null or
// missing)" error from the issue-11 path — operators upgrading from
// the v1 schema see a clear "this field is missing" message rather
// than the opaque `unknown action ""` we used to emit.
func TestPGMigration_OldFieldNames(t *testing.T) {
	type yamlPickPolicy struct {
		ItemsTable               string        `yaml:"items_table"`
		OnCommit                 action.Action `yaml:"on_commit"`
		OnGiveUp                 action.Action `yaml:"on_give_up"`
		VisibilityTimeoutSeconds int           `yaml:"visibility_timeout_seconds"`
	}
	const oldYAML = `
items_table: items
on_commit_default: release_to_back
on_give_up_default: delete
visibility_timeout_seconds: 60
`
	var pp yamlPickPolicy
	if err := yaml.Unmarshal([]byte(oldYAML), &pp); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	// Old field names dropped silently by yaml.v3 — actions stay zero.
	if pp.OnCommit.Kind != "" {
		t.Errorf("expected OnCommit.Kind empty (old field name ignored); got %q", pp.OnCommit.Kind)
	}
	// Validator catches the zero-Kind.
	internalPP := &PickPolicy{
		ItemsTable: pp.ItemsTable,
		OnCommit:   pp.OnCommit,
		OnGiveUp:   pp.OnGiveUp,
	}
	res := validatePickPolicy("@q", internalPP)
	if res.OK() {
		t.Fatal("expected validation error after old-field-name parse")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "required (got null or missing)") {
		t.Errorf("expected 'required (got null or missing)' error; got %q", joined)
	}
}

// startTestPostgres spins up a throwaway postgres container for the
// SQL-path tests below. Returns a connection pool + DSN. The
// container is torn down via t.Cleanup. Mirrors the
// foundation/internal/pgtest helper but lives here so this test file
// can stay inside the stores/ depguard envelope without reaching
// across modules.
func startTestPostgres(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	container, err := pgmodule.Run(ctx,
		"postgres:14-alpine",
		pgmodule.WithDatabase("rimsky"),
		pgmodule.WithUsername("rimsky"),
		pgmodule.WithPassword("rimsky"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("postgres testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() {
		termCtx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = container.Terminate(termCtx)
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, dsn
}

// createTestItemsTable creates an items table matching the schema the
// pg-store expects (mirrors test/smoke/setup.go::createTopicsItemsTable).
func createTestItemsTable(t *testing.T, pool *pgxpool.Pool, table string) {
	t.Helper()
	stmt := fmt.Sprintf(`
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
	`, table, table, table, table, table)
	if _, err := pool.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("create items table %q: %v", table, err)
	}
}

// TestPGAction_Pop_RowDeleted exercises the Pop SQL path
// (`DELETE FROM <items_table> WHERE claim_token = $1`) end-to-end:
// seed → Open → Commit (Pop) → assert row gone → assert second Open
// returns Unavailable. Spec §10.7 mandates this test.
func TestPGAction_Pop_RowDeleted(t *testing.T) {
	pool, dsn := startTestPostgres(t)
	const tbl = "items_pop_test"
	createTestItemsTable(t, pool, tbl)

	// Seed exactly one row.
	if _, err := pool.Exec(context.Background(),
		fmt.Sprintf(`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`, tbl),
		"row-1", `{"v":1}`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	st, err := New(context.Background(), Config{
		Connection:     dsn,
		WriteSemantics: corestore.WriteSemanticsStagedAsync,
		PickPolicies: map[string]*PickPolicy{
			"@q": {
				ItemsTable:        tbl,
				OnCommit:          action.Action{Kind: action.Pop},
				OnGiveUp:          action.Action{Kind: action.Recycle},
				VisibilityTimeout: time.Minute,
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)

	// Open #1: claims row-1.
	o, err := st.Open(context.Background(), "claim-1", "@q")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !o.Available {
		t.Fatal("Open: expected Available")
	}

	// Commit fires Pop → DELETE FROM items_pop_test WHERE claim_token = 'claim-1'.
	if err := st.Commit(context.Background(), "claim-1", o.Result.Scope, o.Result.Address); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Assert: row deleted.
	var count int
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT COUNT(*) FROM %s`, tbl)).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after Pop; got %d", count)
	}

	// Second Open returns Unavailable (table empty).
	o2, err := st.Open(context.Background(), "claim-2", "@q")
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	if o2.Available {
		t.Errorf("Open #2: expected Unavailable on empty table; got Available")
	}
}

// TestPGAction_Recycle_RowReturnsToQueue exercises the Recycle SQL path
// (`UPDATE ... SET state='available', claim_token=NULL, claimed_at=NULL,
// sequence=nextval(...)`) end-to-end. Seed → Open → Commit (Recycle) →
// assert row returned to 'available' with claim_token cleared and a
// fresh sequence value (so the row tail-rotates on FIFO ordering).
// Spec §10.7 mandates this test.
func TestPGAction_Recycle_RowReturnsToQueue(t *testing.T) {
	pool, dsn := startTestPostgres(t)
	const tbl = "items_recycle_test"
	createTestItemsTable(t, pool, tbl)

	// Seed one row.
	if _, err := pool.Exec(context.Background(),
		fmt.Sprintf(`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`, tbl),
		"row-1", `{"v":1}`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	st, err := New(context.Background(), Config{
		Connection:     dsn,
		WriteSemantics: corestore.WriteSemanticsStagedAsync,
		PickPolicies: map[string]*PickPolicy{
			"@q": {
				ItemsTable:        tbl,
				OnCommit:          action.Action{Kind: action.Recycle},
				OnGiveUp:          action.Action{Kind: action.Recycle},
				VisibilityTimeout: time.Minute,
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)

	// Capture the original sequence so we can verify it changes.
	var seqBefore int64
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT sequence FROM %s WHERE item_id='row-1'`, tbl)).Scan(&seqBefore); err != nil {
		t.Fatalf("capture seqBefore: %v", err)
	}

	// Open #1: claims row-1.
	o, err := st.Open(context.Background(), "claim-1", "@q")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !o.Available {
		t.Fatal("Open: expected Available")
	}

	// Commit fires Recycle.
	if err := st.Commit(context.Background(), "claim-1", o.Result.Scope, o.Result.Address); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Assert: row back to 'available', claim_token cleared, sequence bumped.
	var (
		state      string
		claimToken *string
		claimedAt  *time.Time
		seqAfter   int64
	)
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT state, claim_token, claimed_at, sequence FROM %s WHERE item_id='row-1'`, tbl),
	).Scan(&state, &claimToken, &claimedAt, &seqAfter); err != nil {
		t.Fatalf("scan after recycle: %v", err)
	}
	if state != "available" {
		t.Errorf("state after Recycle = %q, want %q", state, "available")
	}
	if claimToken != nil {
		t.Errorf("claim_token after Recycle = %v, want nil", *claimToken)
	}
	if claimedAt != nil {
		t.Errorf("claimed_at after Recycle = %v, want nil", *claimedAt)
	}
	if seqAfter <= seqBefore {
		t.Errorf("sequence after Recycle = %d, expected > %d (nextval bumps it)", seqAfter, seqBefore)
	}

	// Second Open re-claims the same row (still the only one).
	o2, err := st.Open(context.Background(), "claim-2", "@q")
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	if !o2.Available {
		t.Errorf("Open #2: expected Available (row recycled); got Unavailable")
	}
}

// TestPGYAMLAcceptsNewVocabulary pins the positive case: the new
// inline vocabulary parses cleanly and validator accepts it.
func TestPGYAMLAcceptsNewVocabulary(t *testing.T) {
	type yamlPickPolicy struct {
		ItemsTable               string        `yaml:"items_table"`
		OnCommit                 action.Action `yaml:"on_commit"`
		OnGiveUp                 action.Action `yaml:"on_give_up"`
		VisibilityTimeoutSeconds int           `yaml:"visibility_timeout_seconds"`
	}
	const newYAML = `
items_table: items
on_commit: pop
on_give_up: recycle
visibility_timeout_seconds: 60
`
	var pp yamlPickPolicy
	if err := yaml.Unmarshal([]byte(newYAML), &pp); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if pp.OnCommit.Kind != action.Pop {
		t.Errorf("OnCommit.Kind = %q, want %q", pp.OnCommit.Kind, action.Pop)
	}
	if pp.OnGiveUp.Kind != action.Recycle {
		t.Errorf("OnGiveUp.Kind = %q, want %q", pp.OnGiveUp.Kind, action.Recycle)
	}
}
