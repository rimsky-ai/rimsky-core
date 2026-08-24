// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func TestPattern_RingMode_LiveDiscovery(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, sub, "beta"), 0o755))
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	for i := 0; i < 4; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d: expected Available", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-%d", i), o.Result.ClaimScope, o.Result.Address, ""))
	}
	must(t, os.MkdirAll(filepath.Join(root, sub, "gamma"), 0o755))
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c2-%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d (post-add): expected Available", i)
		}
		var p struct{ Folder string }
		must(t, json.Unmarshal(o.Result.Payload, &p))
		seen[p.Folder] = true
		must(t, st.Commit(context.Background(), fmt.Sprintf("c2-%d", i), o.Result.ClaimScope, o.Result.Address, ""))
	}
	if !seen["gamma"] {
		t.Errorf("expected gamma to be discovered post-add; saw %v", seen)
	}
}

func TestPattern_OneShotIngest(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	for _, name := range []string{"a", "b", "c"} {
		must(t, os.MkdirAll(filepath.Join(root, sub, name), 0o755))
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.PopAndDelete},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_drain",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d: expected Available", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-%d", i), o.Result.ClaimScope, o.Result.Address, ""))
	}
	o, err := st.Open(context.Background(), "x", "@r")
	must(t, err)
	if o.Available {
		t.Fatal("expected Unavailable after drain")
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, err := os.Stat(filepath.Join(root, sub, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("expected folder %s to be deleted; stat err = %v", name, err)
		}
	}
}

func TestPattern_StaticQueue_ExplicitRefresh(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	for _, name := range []string{"a", "b"} {
		must(t, os.MkdirAll(filepath.Join(root, sub, name), 0o755))
	}
	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "explicit",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	o, err := st.Open(context.Background(), "c-0", "@r")
	must(t, err)
	if o.Available {
		t.Fatal("expected Unavailable until explicit sync")
	}

	must(t, st.runSync("@r", pp))

	for i := 0; i < 2; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-%d", i+1), "@r")
		must(t, err)
		if !o.Available {
			t.Fatalf("iter %d: expected Available", i)
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-%d", i+1), o.Result.ClaimScope, o.Result.Address, ""))
	}
	for i := 0; i < 3; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-stick-%d", i), "@r")
		must(t, err)
		if o.Available {
			t.Fatalf("iter %d post-drain: expected sticky Unavailable", i)
		}
	}
	must(t, st.runSync("@r", pp))
	o, err = st.Open(context.Background(), "c-after-sync", "@r")
	must(t, err)
	if !o.Available {
		t.Fatal("expected Available after operator sync")
	}
}
