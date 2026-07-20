// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

func newBatchLeaseStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "queue", "item-a"), 0o755))
	pp := &PickPolicy{
		Root:              "queue",
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_drain",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@q": pp}})
	must(t, err)
	return st, root
}

func inProgressEntries(t *testing.T, root string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".fs-store", "q", "in_progress"))
	must(t, err)
	return entries
}

func TestBatchLease_StaleLeaseTokenCannotResolveSuccessorLease(t *testing.T) {
	st, root := newBatchLeaseStore(t)
	ctx := context.Background()

	first, err := st.BatchPop(ctx, "@q", []string{"lease-1"})
	must(t, err)
	if len(first) != 1 || first[0].LeaseToken == "" {
		t.Fatalf("BatchPop #1 = %+v, want one item carrying a non-empty lease token", first)
	}
	must(t, st.Abandon(ctx, "unknown-handle-1", first[0].ClaimScopeBytes, nil, first[0].LeaseToken))
	if got := len(inProgressEntries(t, root)); got != 0 {
		t.Fatalf("in_progress after recycle-abandon = %d entries, want 0", got)
	}

	second, err := st.BatchPop(ctx, "@q", []string{"lease-2"})
	must(t, err)
	if len(second) != 1 || second[0].LeaseToken == "" {
		t.Fatalf("BatchPop #2 = %+v, want one item carrying a non-empty lease token", second)
	}
	if second[0].LeaseToken == first[0].LeaseToken {
		t.Fatalf("successor lease minted the same token %q as the stale lease", second[0].LeaseToken)
	}
	if string(second[0].ClaimScopeBytes) != string(first[0].ClaimScopeBytes) {
		t.Fatalf("test premise broken: both leases must land on the byte-equal folder scope (got %s vs %s)",
			second[0].ClaimScopeBytes, first[0].ClaimScopeBytes)
	}

	must(t, st.Commit(ctx, "stale-handle", first[0].ClaimScopeBytes, nil, first[0].LeaseToken))
	if got := len(inProgressEntries(t, root)); got != 1 {
		t.Fatalf("stale-token Commit resolved the successor's live lease: in_progress = %d entries, want 1", got)
	}

	must(t, st.Commit(ctx, "current-handle", second[0].ClaimScopeBytes, nil, second[0].LeaseToken))
	if got := len(inProgressEntries(t, root)); got != 0 {
		t.Fatalf("current-token Commit must resolve the lease: in_progress = %d entries, want 0", got)
	}
}

func TestBatchLease_EmptyTokenKeepsScopeOnlyResolution(t *testing.T) {
	st, root := newBatchLeaseStore(t)
	ctx := context.Background()

	items, err := st.BatchPop(ctx, "@q", []string{"lease-1"})
	must(t, err)
	if len(items) != 1 {
		t.Fatalf("BatchPop = %d items, want 1", len(items))
	}
	must(t, st.Commit(ctx, "rimsky-handle", items[0].ClaimScopeBytes, nil, ""))
	if got := len(inProgressEntries(t, root)); got != 0 {
		t.Fatalf("empty-token Commit must fall back to scope resolution: in_progress = %d entries, want 0", got)
	}
}

func TestFindByScope_MatchesLeaseByTokenNotOrder(t *testing.T) {
	st, root := newBatchLeaseStore(t)
	ctx := context.Background()
	must(t, os.MkdirAll(filepath.Join(root, "queue", "item-b"), 0o755))

	items, err := st.BatchPop(ctx, "@q", []string{"lease-1", "lease-2"})
	must(t, err)
	if len(items) != 2 {
		t.Fatalf("BatchPop = %d items, want 2", len(items))
	}
	for _, item := range items {
		pp, _, entry, folder := st.findByScope(item.ClaimScopeBytes, item.LeaseToken)
		if pp == nil {
			t.Fatalf("findByScope(%s, %s) found nothing", item.ClaimScopeBytes, item.LeaseToken)
		}
		if folder != item.Folder {
			t.Fatalf("findByScope resolved folder %q (entry %q), want %q", folder, entry, item.Folder)
		}
	}
	if pp, _, _, _ := st.findByScope(items[0].ClaimScopeBytes, "batchlease~no-such-lease.1"); pp != nil {
		t.Fatal("findByScope with a token matching no live lease must not resolve any entry")
	}
}
