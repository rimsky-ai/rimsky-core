// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claimledger

import (
	"testing"
	"time"
)

func TestLedgerOpenAndCommit(t *testing.T) {
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

func TestLedgerListPagination(t *testing.T) {
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

func TestLedgerStateFilter(t *testing.T) {
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

func TestLedgerNilSafe(t *testing.T) {
	var l *ClaimLedger
	l.RecordOpen("x", "/y", nil, nil)
	if _, ok := l.Get("x"); ok {
		t.Fatal("nil ledger should not return any records")
	}
	out, next := l.List("", "", 10)
	if out != nil || next != "" {
		t.Fatalf("nil ledger List should return (nil, ''); got %+v %q", out, next)
	}
	l.RecordEvent("x", "claim_commit_failed", "ERROR", nil)
}

func TestLedgerRecordEvent_NonTerminal(t *testing.T) {
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

func TestLedgerEviction_NeverEvictsOpenRecords(t *testing.T) {
	l := NewClaimLedger(3)
	for i := 0; i < 5; i++ {
		l.RecordOpen(string(rune('a'+i)), "/x", nil, nil)
	}
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		if _, ok := l.Get(id); !ok {
			t.Errorf("OPEN record %q was evicted even though every record over the cap is OPEN", id)
		}
	}
	if got := len(l.records); got != 5 {
		t.Fatalf("len(records) = %d, want 5 (cap must be exceeded rather than drop a live claim)", got)
	}
}

func TestLedgerEviction_EvictsClosedRecordsBeforeCap(t *testing.T) {
	l := NewClaimLedger(3)
	l.RecordOpen("closed-1", "/x", nil, nil)
	l.RecordTerminal("closed-1", "claim_committed", nil)
	l.RecordOpen("open-1", "/x", nil, nil)
	l.RecordOpen("open-2", "/x", nil, nil)
	l.RecordOpen("open-3", "/x", nil, nil)

	if _, ok := l.Get("closed-1"); ok {
		t.Fatal("closed record should have been evicted to make room for new OPEN records")
	}
	for _, id := range []string{"open-1", "open-2", "open-3"} {
		if _, ok := l.Get(id); !ok {
			t.Errorf("OPEN record %q must survive eviction", id)
		}
	}
}

func TestLedgerRecordEvent_DefaultSeverity(t *testing.T) {
	l := NewClaimLedger(10)
	l.RecordOpen("c1", "/foo", nil, nil)
	l.RecordEvent("c1", "claim_progress", "", nil)
	rec, _ := l.Get("c1")
	tail := rec.History[len(rec.History)-1]
	if tail.Severity != "INFO" {
		t.Fatalf("tail severity = %q, want INFO (default)", tail.Severity)
	}
}

func TestLedgerSubscribeWithSnapshot_ClosesOnTerminal(t *testing.T) {
	l := NewClaimLedger(10)
	l.RecordOpen("c1", "/foo", nil, nil)
	history, rec, ch, unsub := l.SubscribeWithSnapshot("c1")
	defer unsub()
	if rec == nil || rec.State != ClaimStateOpen {
		t.Fatalf("snapshot record = %+v, want OPEN", rec)
	}
	if len(history) != 1 {
		t.Fatalf("history = %+v, want one claim_opened event", history)
	}
	l.RecordTerminal("c1", "claim_committed", nil)
	ev, ok := <-ch
	if !ok {
		t.Fatal("expected the terminal event on the channel before it closes")
	}
	if ev.Category != "claim_committed" {
		t.Fatalf("event category = %q, want claim_committed", ev.Category)
	}
	if _, ok := <-ch; ok {
		t.Fatal("subscription channel should be closed after the terminal event")
	}
}

func TestLedgerSubscribeWithSnapshot_UnknownClaimClosedChannel(t *testing.T) {
	l := NewClaimLedger(10)
	history, rec, ch, unsub := l.SubscribeWithSnapshot("missing")
	defer unsub()
	if history != nil || rec != nil {
		t.Fatalf("history=%v rec=%v, want nil for an unknown claim", history, rec)
	}
	if _, ok := <-ch; ok {
		t.Fatal("subscription channel for an unknown claim should already be closed")
	}
}
