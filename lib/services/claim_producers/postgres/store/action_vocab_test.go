// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

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
	if pp.OnCommit.Kind != "" {
		t.Errorf("expected OnCommit.Kind empty (old field name ignored); got %q", pp.OnCommit.Kind)
	}
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

func TestPGAction_Pop_RowDeleted(t *testing.T) {
	pool, dsn := bootPostgresTestContainer(t)
	const tbl = "items_pop_test"
	createTestItemsTable(t, pool, tbl)

	if _, err := pool.Exec(context.Background(),
		fmt.Sprintf(`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`, tbl),
		"row-1", `{"v":1}`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	st, err := New(context.Background(), Config{
		Connection:     dsn,
		WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
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

	o, err := st.Open(context.Background(), "claim-1", "@q", claimproducer.IntentReadWrite)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !o.Available {
		t.Fatal("Open: expected Available")
	}

	if err := st.Commit(context.Background(), "claim-1", o.Result.ClaimScope, o.Result.Address); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT COUNT(*) FROM %s`, tbl)).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after Pop; got %d", count)
	}

	o2, err := st.Open(context.Background(), "claim-2", "@q", claimproducer.IntentReadWrite)
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	if o2.Available {
		t.Errorf("Open #2: expected Unavailable on empty table; got Available")
	}
}

func TestPGAction_Recycle_RowReturnsToQueue(t *testing.T) {
	pool, dsn := bootPostgresTestContainer(t)
	const tbl = "items_recycle_test"
	createTestItemsTable(t, pool, tbl)

	if _, err := pool.Exec(context.Background(),
		fmt.Sprintf(`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`, tbl),
		"row-1", `{"v":1}`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	st, err := New(context.Background(), Config{
		Connection:     dsn,
		WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
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

	var seqBefore int64
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT sequence FROM %s WHERE item_id='row-1'`, tbl)).Scan(&seqBefore); err != nil {
		t.Fatalf("capture seqBefore: %v", err)
	}

	o, err := st.Open(context.Background(), "claim-1", "@q", claimproducer.IntentReadWrite)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !o.Available {
		t.Fatal("Open: expected Available")
	}

	if err := st.Commit(context.Background(), "claim-1", o.Result.ClaimScope, o.Result.Address); err != nil {
		t.Fatalf("Commit: %v", err)
	}

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

	o2, err := st.Open(context.Background(), "claim-2", "@q", claimproducer.IntentReadWrite)
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	if !o2.Available {
		t.Errorf("Open #2: expected Available (row recycled); got Unavailable")
	}
}

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
