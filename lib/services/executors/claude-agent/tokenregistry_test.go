// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import "testing"

func makeEntry(runID string) *TokenEntry {
	return &TokenEntry{
		SessionID:         runID,
		AttributesAtSpawn: map[string]any{},
		DispatchContext:   NewDispatchContextSnapshot("d-1", "rs-1", "", "", nil),
		CancelToken:       "ct",
		NodeID:            "n-1",
		CallbackURL:       "http://supervisor.invalid/cb",
		OnComplete: func(map[string]any, bool, *string, []string, ScheduleTeardown) (CompleteResult, error) {
			return CompleteResult{Accepted: true}, nil
		},
		OnBlocked: func(string, any, ScheduleTeardown) error { return nil },
		OnError:   func(string, any, ScheduleTeardown) error { return nil },
	}
}

func TestTokenRegistryRegisterLookupRelease(t *testing.T) {
	reg := NewTokenRegistry()
	entry := makeEntry("run-1")
	reg.Register("tok-1", entry)
	if reg.Size() != 1 {
		t.Fatalf("size = %d, want 1", reg.Size())
	}
	got, ok := reg.Lookup("tok-1")
	if !ok || got != entry {
		t.Fatalf("lookup returned %v, %v", got, ok)
	}
	reg.Release("tok-1")
	if reg.Size() != 0 {
		t.Fatalf("size after release = %d, want 0", reg.Size())
	}
	if _, ok := reg.Lookup("tok-1"); ok {
		t.Fatal("expected lookup miss after release")
	}
}

func TestTokenRegistryLookupUnknownToken(t *testing.T) {
	reg := NewTokenRegistry()
	if _, ok := reg.Lookup("nope"); ok {
		t.Fatal("expected miss for unknown token")
	}
}
