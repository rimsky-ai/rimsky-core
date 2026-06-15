// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"testing"
	"time"
)

func TestPgLedgerOpenAndCommit(t *testing.T) {
	l := NewClaimLedger(10)
	l.RecordOpen("c1", "/foo", []byte(`"a"`), []byte(`"r"`))
	rec, ok := l.Get("c1")
	if !ok {
		t.Fatal("expected c1")
	}
	if rec.State != ClaimStateOpen {
		t.Fatalf("state = %s, want OPEN", rec.State)
	}
	l.RecordTerminal("c1", "claim_committed", nil)
	rec2, _ := l.Get("c1")
	if rec2.State != ClaimStateCommitted {
		t.Fatalf("state = %s, want COMMITTED", rec2.State)
	}
	if rec2.ClosedAt == nil || time.Since(*rec2.ClosedAt) > time.Minute {
		t.Fatalf("closed_at not set sensibly: %+v", rec2.ClosedAt)
	}
}

func TestPgLedgerListPagination(t *testing.T) {
	l := NewClaimLedger(10)
	for i := 0; i < 4; i++ {
		l.RecordOpen("c"+string(rune('0'+i)), "/x", nil, nil)
	}
	page1, next := l.List("", "", 2)
	if len(page1) != 2 || next == "" {
		t.Fatalf("page1=%d next=%q", len(page1), next)
	}
	page2, _ := l.List("", next, 10)
	if len(page2) != 2 {
		t.Fatalf("page2=%d", len(page2))
	}
}

func TestPgLedgerStateFilter(t *testing.T) {
	l := NewClaimLedger(10)
	l.RecordOpen("a", "/x", nil, nil)
	l.RecordOpen("b", "/y", nil, nil)
	l.RecordTerminal("a", "claim_committed", nil)
	got, _ := l.List("OPEN", "", 10)
	if len(got) != 1 || got[0].ClaimID != "b" {
		t.Fatalf("filter OPEN got %+v", got)
	}
	got2, _ := l.List("COMMITTED", "", 10)
	if len(got2) != 1 || got2[0].ClaimID != "a" {
		t.Fatalf("filter COMMITTED got %+v", got2)
	}
}

func TestPgLedgerNilSafe(t *testing.T) {
	var l *ClaimLedger
	l.RecordOpen("x", "/y", nil, nil)
	if _, ok := l.Get("x"); ok {
		t.Fatal("nil ledger should not return any records")
	}
	out, next := l.List("", "", 10)
	if out != nil || next != "" {
		t.Fatalf("nil ledger List should return (nil, ''); got %+v %q", out, next)
	}
	// @constraint: RecordEvent on the nil receiver must also be a no-op.
	l.RecordEvent("x", "claim_commit_failed", "ERROR", nil)
}

// TestPgLedgerRecordEvent_NonTerminal: the non-terminal append path
// (claim_commit_failed / claim_abandon_failed) must surface in
// History without flipping State or ClosedAt — the next retry may
// still succeed, so the claim is not yet closed.
func TestPgLedgerRecordEvent_NonTerminal(t *testing.T) {
	l := NewClaimLedger(10)
	l.RecordOpen("c1", "/foo", []byte(`"a"`), []byte(`"r"`))
	l.RecordEvent("c1", "claim_commit_failed", "ERROR", map[string]any{
		"error": "deadlock",
	})
	rec, ok := l.Get("c1")
	if !ok {
		t.Fatal("missing c1")
	}
	if rec.State != ClaimStateOpen {
		t.Fatalf("State = %s, want OPEN (non-terminal must not flip state)", rec.State)
	}
	if rec.ClosedAt != nil {
		t.Fatalf("ClosedAt = %v, want nil (non-terminal must not stamp closed_at)", rec.ClosedAt)
	}
	if len(rec.History) != 2 {
		t.Fatalf("history len = %d, want 2 (open + commit_failed)", len(rec.History))
	}
	tail := rec.History[1]
	if tail.Category != "claim_commit_failed" || tail.Severity != "ERROR" {
		t.Fatalf("tail = %+v; want category=claim_commit_failed, severity=ERROR", tail)
	}
	if tail.Attributes["error"] != "deadlock" {
		t.Fatalf("tail attrs[error] = %v, want deadlock", tail.Attributes["error"])
	}
	l.RecordTerminal("c1", "claim_committed", nil)
	rec2, _ := l.Get("c1")
	if rec2.State != ClaimStateCommitted {
		t.Fatalf("State after terminal = %s, want COMMITTED", rec2.State)
	}
}

// TestPgLedgerRecordEvent_DefaultSeverity: empty severity defaults
// to "INFO" so the dashboard's severity filter never sees an
// unspecified value.
func TestPgLedgerRecordEvent_DefaultSeverity(t *testing.T) {
	l := NewClaimLedger(10)
	l.RecordOpen("c1", "/foo", nil, nil)
	l.RecordEvent("c1", "claim_progress", "", nil)
	rec, _ := l.Get("c1")
	tail := rec.History[len(rec.History)-1]
	if tail.Severity != "INFO" {
		t.Fatalf("tail severity = %q, want INFO (default)", tail.Severity)
	}
}
