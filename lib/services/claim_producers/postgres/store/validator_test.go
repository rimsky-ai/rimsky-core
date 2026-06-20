// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"strings"
	"testing"
	"time"

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
	cases := []struct {
		name string
		in   any
		want string
	}{
		{name: "string", in: "row-42", want: "row-42"},
		{name: "bytes", in: []byte("abc"), want: "abc"},
		{name: "int", in: int64(7), want: "7"},
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
