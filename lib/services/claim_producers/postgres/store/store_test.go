// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"testing"

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

	if got := st.Ledger().max; got != defaultLedgerMaxRecords {
		t.Fatalf("ledger.max = %d, want default %d", got, defaultLedgerMaxRecords)
	}
}
