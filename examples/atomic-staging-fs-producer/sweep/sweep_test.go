// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package sweep

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/examples/atomic-staging-fs-producer/store"
)

type liveSet struct{ set map[string]struct{} }

func (l liveSet) Contains(claimID string) bool {
	_, ok := l.set[claimID]
	return ok
}

func TestTick_PreservesAliveAndOldLeakedReaped(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	st, err := store.New(tmp)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	// Three claims: alive (in live set), young-leak (not in live set
	// but < TTL old), old-leak (not in live set and > TTL old).
	if _, err := st.Open("alive-1", "scope-a"); err != nil {
		t.Fatalf("Open alive-1: %v", err)
	}
	if _, err := st.Open("young-leak", "scope-y"); err != nil {
		t.Fatalf("Open young-leak: %v", err)
	}
	if _, err := st.Open("old-leak", "scope-o"); err != nil {
		t.Fatalf("Open old-leak: %v", err)
	}

	// Drive Tick at a fake "now" 25h after the old-leak's creation; the
	// young-leak's CreatedAt is in the past too but we set TTL to 24h so
	// it must survive. Strategy: rewrite the side-table's old-leak entry
	// with a back-dated CreatedAt.
	backdateOldLeak(t, tmp)

	sw := &Sweeper{
		Store: st,
		Live:  liveSet{set: map[string]struct{}{"alive-1": {}}},
		TTL:   24 * time.Hour,
	}
	if err := sw.Tick(time.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	entries, err := st.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	// alive-1 must remain; young-leak must remain; old-leak must be gone.
	var alive, young, old bool
	for _, e := range entries {
		switch e.ClaimID {
		case "alive-1":
			alive = true
		case "young-leak":
			young = true
		case "old-leak":
			old = true
		}
	}
	if !alive {
		t.Errorf("alive-1 must remain after sweep")
	}
	if !young {
		t.Errorf("young-leak must remain after sweep (within TTL)")
	}
	if old {
		t.Errorf("old-leak must be reaped after sweep (older than TTL)")
	}

	// Staging dir on disk: old-leak's dir gone; others present.
	for _, c := range []struct {
		id    string
		scope string
		want  bool
	}{
		{"alive-1", "scope-a", true},
		{"young-leak", "scope-y", true},
		{"old-leak", "scope-o", false},
	} {
		path := filepath.Join(tmp, "staging", c.scope, c.id)
		_, err := os.Stat(path)
		exists := !os.IsNotExist(err)
		if exists != c.want {
			t.Errorf("staging %s exists=%v want=%v", c.id, exists, c.want)
		}
	}
}

// backdateOldLeak rewrites the side-table to give the `old-leak` entry
// a 25h-old CreatedAt. The file format is JSON-per-line; reading and
// rewriting in-place is the simplest approach for the test.
func backdateOldLeak(t *testing.T, tmp string) {
	t.Helper()
	statePath := filepath.Join(tmp, "producer_state.jsonl")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	// Naive substring rewrite: find the `old-leak` line and replace its
	// CreatedAt with a 25h-old timestamp. We can use the encoder by
	// loading via store.Entries-equivalent, but the test owns the file
	// shape so a string replace keeps the test simple.
	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	out := []byte(replaceAfterClaim(string(data), "old-leak", old))
	if err := os.WriteFile(statePath, out, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func replaceAfterClaim(s, claimID, newCreatedAt string) string {
	// Lines look like: {"claim_id":"old-leak",...,"created_at":"<old>"}
	// We do the cheapest possible rewrite: find the claim_id and
	// substitute the created_at value to newCreatedAt by reconstructing.
	// Simpler: re-emit the line preserving fields.
	// For test brevity, we just zero out created_at to newCreatedAt
	// in-place via a regex-like split.
	return naiveReplaceCreatedAt(s, claimID, newCreatedAt)
}

// naiveReplaceCreatedAt finds the JSONL line containing `claim_id`
// equal to id and rewrites the `created_at` value to v. Whitespace-
// sensitive; the side-table writer emits without indentation.
func naiveReplaceCreatedAt(blob, id, v string) string {
	out := ""
	for _, line := range splitLines(blob) {
		if !contains(line, `"claim_id":"`+id+`"`) {
			out += line + "\n"
			continue
		}
		out += rewriteCreatedAt(line, v) + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i, ch := range s {
		if ch == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func contains(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func rewriteCreatedAt(line, v string) string {
	key := `"created_at":"`
	i := indexOf(line, key)
	if i < 0 {
		return line
	}
	start := i + len(key)
	end := start
	for end < len(line) && line[end] != '"' {
		end++
	}
	return line[:start] + v + line[end:]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
