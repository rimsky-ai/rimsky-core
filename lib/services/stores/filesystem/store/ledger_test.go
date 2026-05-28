// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLedgerRecordsOpenAndCommit(t *testing.T) {
	root := t.TempDir()
	st, err := New(Config{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	target := filepath.Join(root, "foo")
	_ = target // existence not required for direct-mode Open
	if _, err := st.Open(context.Background(), "claim-1", "foo"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec, ok := st.Ledger().Get("claim-1")
	if !ok {
		t.Fatal("ledger missing claim-1")
	}
	if rec.State != ClaimStateOpen {
		t.Fatalf("state = %s, want OPEN", rec.State)
	}
	if len(rec.History) != 1 || rec.History[0].Category != "claim_opened" {
		t.Fatalf("history = %+v, want one claim_opened event", rec.History)
	}
	if err := st.Commit(context.Background(), "claim-1", nil, nil); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	rec, _ = st.Ledger().Get("claim-1")
	if rec.State != ClaimStateCommitted {
		t.Fatalf("state = %s, want COMMITTED", rec.State)
	}
	if len(rec.History) != 2 || rec.History[1].Category != "claim_committed" {
		t.Fatalf("history = %+v, want claim_opened + claim_committed", rec.History)
	}
}

func TestLedgerListPagination(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	for i := 0; i < 5; i++ {
		_, _ = st.Open(context.Background(), "claim-"+string(rune('a'+i)), "p"+string(rune('0'+i)))
	}
	recs, next := st.Ledger().List("", "", 2)
	if len(recs) != 2 || next == "" {
		t.Fatalf("first page = %d records, next=%q", len(recs), next)
	}
	recs2, _ := st.Ledger().List("", next, 10)
	if len(recs2) != 3 {
		t.Fatalf("second page = %d records, want 3", len(recs2))
	}
}

func TestLedgerGetMissing(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	if _, ok := st.Ledger().Get("missing"); ok {
		t.Fatal("Get(missing) returned ok=true")
	}
}

// TestLedgerRecordEvent_NonTerminal exercises the non-terminal append
// path used by claim_commit_failed / claim_abandon_failed. The
// observation must be appended to the claim's history without
// flipping State or stamping ClosedAt — those are reserved for
// terminal events.
func TestLedgerRecordEvent_NonTerminal(t *testing.T) {
	st, _ := New(Config{Root: t.TempDir()})
	if _, err := st.Open(context.Background(), "claim-1", "foo"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	st.Ledger().RecordEvent("claim-1", "claim_commit_failed", "ERROR", map[string]any{
		"error": "transient",
	})
	rec, ok := st.Ledger().Get("claim-1")
	if !ok {
		t.Fatal("ledger missing claim-1")
	}
	if rec.State != ClaimStateOpen {
		t.Fatalf("State = %s after non-terminal RecordEvent; want OPEN", rec.State)
	}
	if rec.ClosedAt != nil {
		t.Fatalf("ClosedAt = %v after non-terminal RecordEvent; want nil", rec.ClosedAt)
	}
	if len(rec.History) != 2 {
		t.Fatalf("history len = %d, want 2 (claim_opened + claim_commit_failed)", len(rec.History))
	}
	tail := rec.History[len(rec.History)-1]
	if tail.Category != "claim_commit_failed" {
		t.Fatalf("tail category = %q, want claim_commit_failed", tail.Category)
	}
	if tail.Severity != "ERROR" {
		t.Fatalf("tail severity = %q, want ERROR", tail.Severity)
	}
	if got := tail.Attributes["error"]; got != "transient" {
		t.Fatalf("tail attrs[error] = %v, want transient", got)
	}
	// A subsequent terminal event must still flip State / ClosedAt.
	st.Ledger().RecordTerminal("claim-1", "claim_committed", nil)
	rec2, _ := st.Ledger().Get("claim-1")
	if rec2.State != ClaimStateCommitted {
		t.Fatalf("State after terminal = %s, want COMMITTED", rec2.State)
	}
	if rec2.ClosedAt == nil {
		t.Fatal("ClosedAt nil after terminal")
	}
}
