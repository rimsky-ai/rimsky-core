// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package store

import (
	"strings"
	"testing"
	"time"

	"github.com/fallguyconsulting/rimsky/stores/common/action"
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
	pp.OnCommit = action.Action{} // zero
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
