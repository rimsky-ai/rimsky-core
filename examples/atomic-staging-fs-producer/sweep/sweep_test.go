// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package sweep

import (
	"os"
	"path/filepath"
	"strings"
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

	if _, err := st.Open("alive-1", "scope-a"); err != nil {
		t.Fatalf("Open alive-1: %v", err)
	}
	if _, err := st.Open("young-leak", "scope-y"); err != nil {
		t.Fatalf("Open young-leak: %v", err)
	}
	if _, err := st.Open("old-leak", "scope-o"); err != nil {
		t.Fatalf("Open old-leak: %v", err)
	}

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

func backdateOldLeak(t *testing.T, tmp string) {
	t.Helper()
	statePath := filepath.Join(tmp, "producer_state.jsonl")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	out := []byte(naiveReplaceCreatedAt(string(data), "old-leak", old))
	if err := os.WriteFile(statePath, out, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func naiveReplaceCreatedAt(blob, id, v string) string {
	if blob == "" {
		return ""
	}
	needle := `"claim_id":"` + id + `"`
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(blob, "\n"), "\n") {
		if strings.Contains(line, needle) {
			line = rewriteCreatedAt(line, v)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func rewriteCreatedAt(line, v string) string {
	key := `"created_at":"`
	i := strings.Index(line, key)
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
