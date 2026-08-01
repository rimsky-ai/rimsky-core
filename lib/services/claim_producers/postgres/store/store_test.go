// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func TestValidIdent(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"items", true},
		{"my_items", true},
		{"items_42", true},
		{"a", true},
		{"_underscore_lead", true},
		{"", false},
		{"42items", false},
		{"Items", false},
		{"my-table", false},
		{"my.table", false},
		{"my table", false},
		{"items;DROP TABLE", false},
		{"items'--", false},
	}
	for _, tc := range cases {
		t.Run(tc.s, func(t *testing.T) {
			if got := validIdent(tc.s); got != tc.want {
				t.Fatalf("validIdent(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

func TestValidPickAction(t *testing.T) {
	good := []action.Kind{action.Pop, action.Recycle}
	for _, a := range good {
		if !validPickAction(a) {
			t.Errorf("validPickAction(%q) = false, want true", a)
		}
	}
	bad := []action.Kind{
		action.Kind(""),
		action.PopAndMove,
		action.PopAndDelete,
		action.Kind("delete"),
		action.Kind("release_to_back"),
		action.Kind("release_to_head"),
	}
	for _, a := range bad {
		if validPickAction(a) {
			t.Errorf("validPickAction(%q) = true, want false", a)
		}
	}
}

func TestNewRejectsEmptyConnection(t *testing.T) {
	if _, err := New(t.Context(), Config{}); err == nil {
		t.Fatal("New with empty Connection should error; got nil")
	}
}

func TestNew_PingsPoolAndFailsFastOnUnreachableConnection(t *testing.T) {
	if _, err := New(t.Context(), Config{Connection: "postgres://rimsky:rimsky@127.0.0.1:1/rimsky?sslmode=disable&connect_timeout=1"}); err == nil {
		t.Fatal("New with an unreachable connection string should error at startup (a live Ping), not boot a producer that only fails at first Open")
	}
}

func TestNew_VerifiesPartitionPolicyItemsTableExists(t *testing.T) {
	_, dsn := bootPostgresTestContainer(t)
	_, err := New(t.Context(), Config{
		Connection: dsn,
		PartitionPolicies: map[string]*PartitionPolicy{
			"@missing": {ItemsTable: "table_that_does_not_exist", Select: "id", Where: "true"},
		},
	})
	if err == nil {
		t.Fatal("New with a partition_policies items_table that does not exist should error at startup")
	}
}

func TestNew_AcceptsPartitionPolicyWithExistingItemsTable(t *testing.T) {
	pool, dsn := bootPostgresTestContainer(t)
	if _, err := pool.Exec(t.Context(), "CREATE TABLE partition_probe (id TEXT, payload TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	st, err := New(t.Context(), Config{
		Connection: dsn,
		PartitionPolicies: map[string]*PartitionPolicy{
			"@ok": {ItemsTable: "partition_probe", Select: "id", Where: "true"},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)
}

func TestInsertItems_MidBatchFailureRollsBackWholeBatch(t *testing.T) {
	pool, dsn := bootPostgresTestContainer(t)
	if _, err := pool.Exec(t.Context(), `CREATE TABLE dup_items (
		item_id     TEXT PRIMARY KEY,
		payload     JSONB NOT NULL,
		state       TEXT NOT NULL,
		claim_token TEXT,
		claimed_at  TIMESTAMPTZ,
		enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		priority    INT NOT NULL DEFAULT 0,
		sequence    BIGSERIAL,
		UNIQUE (payload)
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	st, err := New(t.Context(), Config{
		Connection: dsn,
		PickPolicies: map[string]*PickPolicy{
			"@dup": {
				ItemsTable:        "dup_items",
				OnCommit:          action.Action{Kind: action.Pop},
				OnGiveUp:          action.Action{Kind: action.Pop},
				VisibilityTimeout: time.Minute,
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)

	payloads := []json.RawMessage{
		json.RawMessage(`{"v":1}`),
		json.RawMessage(`{"v":1}`),
	}
	if err := st.InsertItems(t.Context(), "@dup", payloads); err == nil {
		t.Fatal("InsertItems: expected an error from the UNIQUE violation on the second row, got nil")
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM dup_items").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("rows in dup_items = %d, want 0 (a mid-batch failure must roll back the whole batch, not leave a partial insert committed)", count)
	}
}

func TestNew_LedgerMaxRecordsConfigured(t *testing.T) {
	_, dsn := bootPostgresTestContainer(t)
	st, err := New(t.Context(), Config{Connection: dsn, LedgerMaxRecords: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)

	st.Ledger().RecordOpen("closed-1", "/x", nil, nil)
	st.Ledger().RecordTerminal("closed-1", "claim_committed", nil)
	st.Ledger().RecordOpen("open-2", "/x", nil, nil)
	st.Ledger().RecordOpen("open-3", "/x", nil, nil)

	if _, ok := st.Ledger().Get("closed-1"); ok {
		t.Fatal("closed-1 should have been evicted once the configured cap (2) was exceeded")
	}
	if _, ok := st.Ledger().Get("open-2"); !ok {
		t.Error("open-2 must survive eviction")
	}
	if _, ok := st.Ledger().Get("open-3"); !ok {
		t.Error("open-3 must survive eviction")
	}
}

func TestNew_LedgerMaxRecordsDefaultsTo1024(t *testing.T) {
	_, dsn := bootPostgresTestContainer(t)
	st, err := New(t.Context(), Config{Connection: dsn})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)

	for i := 0; i < defaultLedgerMaxRecords; i++ {
		id := fmt.Sprintf("closed-%d", i)
		st.Ledger().RecordOpen(id, "/x", nil, nil)
		st.Ledger().RecordTerminal(id, "claim_committed", nil)
	}
	if _, ok := st.Ledger().Get("closed-0"); !ok {
		t.Fatal("closed-0 should still be present with exactly defaultLedgerMaxRecords entries recorded")
	}

	st.Ledger().RecordOpen("closed-overflow", "/x", nil, nil)
	st.Ledger().RecordTerminal("closed-overflow", "claim_committed", nil)

	if _, ok := st.Ledger().Get("closed-0"); ok {
		t.Fatal("closed-0 should have been evicted once defaultLedgerMaxRecords was exceeded")
	}
	if _, ok := st.Ledger().Get("closed-overflow"); !ok {
		t.Fatal("closed-overflow must survive eviction")
	}
}
