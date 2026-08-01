// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func newPGValidPolicy() *PickPolicy {
	return &PickPolicy{
		ItemsTable:        "items",
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
	}
}

func errsString(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

func TestPGValidator_RejectsPopAndMove(t *testing.T) {
	pp := newPGValidPolicy()
	pp.OnCommit = action.Action{Kind: action.PopAndMove, MoveTarget: "x"}
	res := validatePickPolicy("@q", pp)
	if res.OK() {
		t.Fatal("expected error for pop_and_move on pg-store")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "not supported by postgres store") {
		t.Errorf("expected 'not supported by postgres store' error; got %q", joined)
	}
}

func TestPGValidator_RejectsPopAndDelete(t *testing.T) {
	pp := newPGValidPolicy()
	pp.OnCommit = action.Action{Kind: action.PopAndDelete}
	res := validatePickPolicy("@q", pp)
	if res.OK() {
		t.Fatal("expected error for pop_and_delete on pg-store")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "not supported by postgres store") {
		t.Errorf("expected 'not supported by postgres store' error; got %q", joined)
	}
}

func TestPGValidator_RejectsOldNames(t *testing.T) {
	pp := newPGValidPolicy()
	pp.OnCommit = action.Action{Kind: action.Kind("release_to_back")}
	res := validatePickPolicy("@q", pp)
	if res.OK() {
		t.Fatal("expected error for old release_to_back")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "unknown action") {
		t.Errorf("expected 'unknown action' error; got %q", joined)
	}
}

func TestPGValidator_RejectsMissingFields(t *testing.T) {
	pp := newPGValidPolicy()
	pp.OnCommit = action.Action{}
	res := validatePickPolicy("@q", pp)
	if res.OK() {
		t.Fatal("expected error for missing OnCommit")
	}
}

func TestPGValidator_RejectsBadIdent(t *testing.T) {
	pp := newPGValidPolicy()
	pp.ItemsTable = "Bad-Ident"
	res := validatePickPolicy("@q", pp)
	if res.OK() {
		t.Fatal("expected error for invalid identifier")
	}
	joined := errsString(res.Errors)
	if !strings.Contains(joined, "items_table") {
		t.Errorf("expected items_table error; got %q", joined)
	}
}

func TestPGValidator_AcceptsPopAndRecycle(t *testing.T) {
	pp := newPGValidPolicy()
	pp.OnCommit = action.Action{Kind: action.Pop}
	pp.OnGiveUp = action.Action{Kind: action.Recycle}
	res := validatePickPolicy("@q", pp)
	if !res.OK() {
		t.Fatalf("expected OK for pop+recycle; got: %v", res.Errors)
	}
}

func TestPGValidator_RejectsZeroVisibility(t *testing.T) {
	pp := newPGValidPolicy()
	pp.VisibilityTimeout = 0
	res := validatePickPolicy("@q", pp)
	if res.OK() {
		t.Fatal("expected error for zero VisibilityTimeout")
	}
}

func TestPartitionPolicy_RejectsPlaceholdersWithoutParamOrder(t *testing.T) {
	pp := &PartitionPolicy{
		ItemsTable: "items",
		Select:     "id, status",
		Where:      "status = $1 AND created_at > $2",
		ParamOrder: nil,
	}
	err := validatePartitionPolicy("@bad", pp)
	if err == nil {
		t.Fatal("expected error for $N placeholders without params_schema")
	}
	if !strings.Contains(err.Error(), "params_schema") {
		t.Errorf("expected message to mention params_schema; got %q", err.Error())
	}
}

func TestPartitionPolicy_AcceptsPlaceholdersWithParamOrder(t *testing.T) {
	pp := &PartitionPolicy{
		ItemsTable: "items",
		Select:     "id, status",
		Where:      "status = $1",
		ParamOrder: []string{"status"},
	}
	if err := validatePartitionPolicy("@ok", pp); err != nil {
		t.Fatalf("expected no error; got %v", err)
	}
}

func TestCanonicalRowID_Variants(t *testing.T) {
	fixedUUID := uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	cases := []struct {
		name string
		in   any
		want string
	}{
		{name: "string", in: "row-42", want: "row-42"},
		{name: "bytes", in: []byte("abc"), want: "abc"},
		{name: "int", in: int64(7), want: "7"},
		{name: "uuid", in: [16]byte(fixedUUID), want: fixedUUID.String()},
		{name: "numeric", in: pgtype.Numeric{Int: big.NewInt(4250), Exp: -2, Valid: true}, want: "42.50"},
	}
	for _, tc := range cases {
		got, err := canonicalRowID(tc.in)
		if err != nil {
			t.Errorf("canonicalRowID(%v): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("canonicalRowID(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanonicalRowID_RejectsNullNumeric(t *testing.T) {
	if _, err := canonicalRowID(pgtype.Numeric{Valid: false}); err == nil {
		t.Fatal("canonicalRowID(NULL numeric): expected error, got nil")
	}
}

func TestNormalizePolicyValue_UUIDAndNumericAreNotByteArrays(t *testing.T) {
	fixedUUID := uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	if got := normalizePolicyValue([16]byte(fixedUUID)); got != fixedUUID.String() {
		t.Fatalf("normalizePolicyValue(uuid bytes) = %v (%T), want string %q", got, got, fixedUUID.String())
	}
	got := normalizePolicyValue(pgtype.Numeric{Int: big.NewInt(4250), Exp: -2, Valid: true})
	if _, ok := got.(json.Number); !ok {
		t.Fatalf("normalizePolicyValue(numeric) = %v (%T), want json.Number", got, got)
	}
}

func TestRunPartitionPolicy_UUIDPrimaryKeyYieldsCanonicalStringID(t *testing.T) {
	pool, _ := bootPostgresTestContainer(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE uuid_items (
			id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			payload TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create extension/table: %v", err)
	}
	var wantID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO uuid_items (payload) VALUES ('body') RETURNING id`,
	).Scan(&wantID); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	s := NewForTest()
	s.SetPoolForTest(pool)
	pp := &PartitionPolicy{
		ItemsTable: "uuid_items",
		Select:     "id, payload",
		Where:      "true",
		Limit:      10,
	}
	rows, err := s.RunPartitionPolicy(ctx, pp, nil)
	if err != nil {
		t.Fatalf("RunPartitionPolicy: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].ID != wantID {
		t.Fatalf("PolicyRow.ID = %q, want canonical uuid text %q", rows[0].ID, wantID)
	}
	var decoded map[string]any
	if err := json.Unmarshal(rows[0].RowJSON, &decoded); err != nil {
		t.Fatalf("RowJSON not valid JSON: %v; got %s", err, rows[0].RowJSON)
	}
	idField, ok := decoded["id"].(string)
	if !ok {
		t.Fatalf("RowJSON[\"id\"] = %v (%T), want a JSON string (not a byte-array)", decoded["id"], decoded["id"])
	}
	if idField != wantID {
		t.Fatalf("RowJSON[\"id\"] = %q, want %q", idField, wantID)
	}
}

func TestRunPartitionPolicy_RejectsDuplicateRowIDs(t *testing.T) {
	pool, _ := bootPostgresTestContainer(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE dup_items (
			id      TEXT,
			payload TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, payload := range []string{"body-0", "body-1"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO dup_items (id, payload) VALUES ('same-id', $1)`, payload,
		); err != nil {
			t.Fatalf("seed row %q: %v", payload, err)
		}
	}

	s := NewForTest()
	s.SetPoolForTest(pool)
	pp := &PartitionPolicy{
		ItemsTable: "dup_items",
		Select:     "id, payload",
		Where:      "true",
		Limit:      10,
	}
	_, err := s.RunPartitionPolicy(ctx, pp, nil)
	if err == nil {
		t.Fatal("RunPartitionPolicy: expected error for duplicate row ids, got nil")
	}
	if !strings.Contains(err.Error(), "same-id") {
		t.Errorf("error message %q should name the duplicated id", err.Error())
	}
}

func TestRunPartitionPolicy_RejectsParamsWithoutOrder(t *testing.T) {
	s := NewForTest()
	pp := &PartitionPolicy{
		ItemsTable: "items",
		Select:     "id",
		Where:      "status = $1",
		ParamOrder: nil,
	}
	_, err := s.RunPartitionPolicy(context.TODO(), pp, map[string]any{"status": "open"})
	if err == nil {
		t.Fatal("expected error for params without ParamOrder")
	}
	if !strings.Contains(err.Error(), "params_schema") {
		t.Errorf("expected message to mention params_schema; got %q", err.Error())
	}
}
