// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	pgmodule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func newLedgerOnlyServerWithClaim(t *testing.T, claimID, selector, rowID string) *Server {
	t.Helper()
	st := pgsstore.NewForTest()
	scope, err := json.Marshal(rowID)
	if err != nil {
		t.Fatalf("marshal scope: %v", err)
	}
	st.Ledger().RecordOpen(claimID, selector, scope, scope)
	return &Server{store: st}
}

func TestSplitScope_PgListShape(t *testing.T) {
	srv := newLedgerOnlyServerWithClaim(t, "p1", "@queue", "row-parent")

	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"list":[{"key":"a","payload":{"v":1}},{"key":"b","payload":{"v":2}}]}`),
	}
	resp, err := srv.SplitScope(context.Background(), req)
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if got, want := len(resp.SubScopes), 2; got != want {
		t.Fatalf("SubScopes count = %d, want %d", got, want)
	}
	wantPayloads := map[string][]byte{
		"a": []byte(`{"v":1}`),
		"b": []byte(`{"v":2}`),
	}
	for _, sub := range resp.SubScopes {
		want, ok := wantPayloads[sub.PartitionKey]
		if !ok {
			t.Errorf("unexpected PartitionKey: %q", sub.PartitionKey)
			continue
		}
		if !bytes.Equal(sub.Payload, want) {
			t.Errorf("PartitionKey=%q: Payload = %s, want %s",
				sub.PartitionKey, string(sub.Payload), string(want))
		}
		if len(sub.Address) != 0 {
			t.Errorf("PartitionKey=%q: Address should be empty for list shape, got %s",
				sub.PartitionKey, string(sub.Address))
		}
		var scope map[string]string
		if err := json.Unmarshal(sub.ClaimScopeData, &scope); err != nil {
			t.Errorf("PartitionKey=%q: ClaimScopeData not a JSON object: %v", sub.PartitionKey, err)
			continue
		}
		if scope["parent_row_id"] != "row-parent" {
			t.Errorf("PartitionKey=%q: ClaimScopeData.parent_row_id = %q, want %q", sub.PartitionKey, scope["parent_row_id"], "row-parent")
		}
		if scope["key"] != sub.PartitionKey {
			t.Errorf("PartitionKey=%q: ClaimScopeData.key = %q, want %q", sub.PartitionKey, scope["key"], sub.PartitionKey)
		}
	}
}

func TestSplitScope_PgListRejectsMalformed(t *testing.T) {
	srv := newLedgerOnlyServerWithClaim(t, "p1", "@queue", "row-parent")
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"list":[{"key":"","payload":{"v":1}}]}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for empty key, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected status error, got %T", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", st.Code())
	}
}

func TestSplitScope_PgUnknownDiscriminatorRejected(t *testing.T) {
	srv := newLedgerOnlyServerWithClaim(t, "p1", "@queue", "row-parent")
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"banana":{}}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for unknown discriminator, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected status error, got %T", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", st.Code())
	}
	msg := st.Message()
	for _, want := range []string{"list", "partition_policy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should mention %q", msg, want)
		}
	}
}

func TestSplitScope_PgUnknownParentClaimRejected(t *testing.T) {
	srv := &Server{store: pgsstore.NewForTest()}
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "nonexistent",
		PartitionRequest: []byte(`{"list":[{"key":"a"}]}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected NotFound for unknown claim, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected status error, got %T", err)
	}
	if st.Code() != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", st.Code())
	}
}

func TestSplitScope_PartitionPolicyUndeclaredRejected(t *testing.T) {
	srv := newLedgerOnlyServerWithClaim(t, "p1", "@queue", "row-parent")
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"partition_policy":"@undeclared","params":{}}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for undeclared partition_policy, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected status error, got %T", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", st.Code())
	}
	if !strings.Contains(st.Message(), "@undeclared") {
		t.Errorf("message %q should mention @undeclared", st.Message())
	}
}

func TestSplitScope_PartitionPolicyParamsNotObjectRejected(t *testing.T) {
	srv := newLedgerOnlyServerWithClaim(t, "p1", "@queue", "row-parent")
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"partition_policy":"@x","params":[]}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for non-object params, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected status error, got %T", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", st.Code())
	}
}

func TestSplitScope_PartitionPolicyMissingParamsRejected(t *testing.T) {
	srv := newLedgerOnlyServerWithClaim(t, "p1", "@queue", "row-parent")
	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "p1",
		PartitionRequest: []byte(`{"partition_policy":"@open"}`),
	}
	_, err := srv.SplitScope(context.Background(), req)
	if err == nil {
		t.Fatal("SplitScope: expected error for missing params, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected status error, got %T", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", st.Code())
	}
	if !strings.Contains(st.Message(), "params") {
		t.Errorf("message %q should mention params", st.Message())
	}
}

func startSplitScopePostgres(t *testing.T) (*pgxpool.Pool, string) {
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

func TestSplitScope_PartitionPolicyShape(t *testing.T) {
	pool, _ := startSplitScopePostgres(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE test_items (
			id      TEXT PRIMARY KEY,
			status  TEXT NOT NULL,
			payload TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO test_items (id, status, payload) VALUES ($1, $2, $3)`,
			fmt.Sprintf("item-%d", i), "open", fmt.Sprintf("body-%d", i),
		); err != nil {
			t.Fatalf("seed item %d: %v", i, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO test_items (id, status, payload) VALUES ($1, $2, $3)`,
		"item-other", "closed", "ignored",
	); err != nil {
		t.Fatalf("seed closed item: %v", err)
	}

	st := pgsstore.NewForTest()
	st.SetPoolForTest(pool)
	st.SetPartitionPoliciesForTest(map[string]*pgsstore.PartitionPolicy{
		"@test_items": {
			ItemsTable: "test_items",
			Select:     "id, status, payload",
			Where:      "status = $1",
			ParamOrder: []string{"status"},
			Limit:      100,
		},
	})
	scope, _ := json.Marshal("row-parent")
	st.Ledger().RecordOpen("parent", "@queue", scope, scope)
	srv := &Server{store: st}

	req := &genv1.SplitScopeRequest{
		ClaimHandleId:    "parent",
		PartitionRequest: []byte(`{"partition_policy":"@test_items","params":{"status":"open"}}`),
	}
	resp, err := srv.SplitScope(ctx, req)
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if got, want := len(resp.SubScopes), 5; got != want {
		t.Fatalf("SubScopes = %d, want %d", got, want)
	}
	seen := make(map[string]bool)
	for _, sub := range resp.SubScopes {
		seen[sub.PartitionKey] = true
		if len(sub.Address) != 0 {
			t.Errorf("PartitionKey=%q: Address should be nil for partition_policy shape, got %s",
				sub.PartitionKey, string(sub.Address))
		}
		var scopeStr string
		if err := json.Unmarshal(sub.ClaimScopeData, &scopeStr); err != nil {
			t.Errorf("PartitionKey=%q: ClaimScopeData not a JSON string: %v", sub.PartitionKey, err)
			continue
		}
		if scopeStr != sub.PartitionKey {
			t.Errorf("PartitionKey=%q: ClaimScopeData = %q, want it to match the row id",
				sub.PartitionKey, scopeStr)
		}
		var row map[string]any
		if err := json.Unmarshal(sub.Payload, &row); err != nil {
			t.Errorf("PartitionKey=%q: Payload not a JSON object: %v (raw=%s)",
				sub.PartitionKey, err, string(sub.Payload))
			continue
		}
		if row["id"] != sub.PartitionKey {
			t.Errorf("PartitionKey=%q: Payload.id = %v, want %q", sub.PartitionKey, row["id"], sub.PartitionKey)
		}
		if row["status"] != "open" {
			t.Errorf("PartitionKey=%q: Payload.status = %v, want \"open\"", sub.PartitionKey, row["status"])
		}
		if row["payload"] == nil {
			t.Errorf("PartitionKey=%q: Payload.payload missing", sub.PartitionKey)
		}
	}
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("item-%d", i)
		if !seen[key] {
			t.Errorf("missing expected PartitionKey %q", key)
		}
	}
	if seen["item-other"] {
		t.Errorf("PartitionKey=item-other should not be present (filtered by status=open)")
	}
}
