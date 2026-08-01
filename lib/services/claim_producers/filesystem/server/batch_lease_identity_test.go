// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestSplitScope_BatchPickLeaseTokenGuardsTerminalVerbs(t *testing.T) {
	srv, _, root := newServerWithPickPolicyAndSync(t, "queue", action.Action{Kind: action.Pop}, "on_drain")
	for _, name := range []string{"f0", "f1"} {
		if err := os.MkdirAll(filepath.Join(root, "queue", name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	ctx := context.Background()
	if _, err := srv.Open(ctx, &genv1.OpenRequest{ClaimId: "parent", Selector: "@queue", Intent: "rw"}); err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	split := func() *genv1.SubScopeDescriptor {
		t.Helper()
		resp, err := srv.SplitScope(ctx, &genv1.SplitScopeRequest{
			ClaimHandleId:    "parent",
			PartitionRequest: []byte(`{"batch_pick":{"max_items":1}}`),
		})
		if err != nil {
			t.Fatalf("SplitScope: %v", err)
		}
		if len(resp.SubScopes) != 1 {
			t.Fatalf("SubScopes count = %d, want 1", len(resp.SubScopes))
		}
		if resp.SubScopes[0].GetLeaseToken() == "" {
			t.Fatal("batch_pick descriptor must carry a non-empty lease_token")
		}
		return resp.SubScopes[0]
	}

	subEntries := func(folder string) int {
		t.Helper()
		entries, err := os.ReadDir(filepath.Join(root, ".fs-store", "queue", "in_progress"))
		if err != nil {
			t.Fatalf("readdir in_progress: %v", err)
		}
		n := 0
		for _, e := range entries {
			f, _, _, perr := fsstore.ParseFromRight(e.Name())
			if perr == nil && f == folder {
				n++
			}
		}
		return n
	}

	staleLease := split()
	if _, err := srv.Abandon(ctx, &genv1.AbandonRequest{
		ClaimId:    "rimsky-sub-1",
		ClaimScope: staleLease.ClaimScopeData,
		LeaseToken: staleLease.GetLeaseToken(),
	}); err != nil {
		t.Fatalf("Abandon stale lease: %v", err)
	}

	liveLease := split()
	if liveLease.GetLeaseToken() == staleLease.GetLeaseToken() {
		t.Fatalf("successor lease reused token %q", liveLease.GetLeaseToken())
	}
	liveFolder := liveLease.GetPartitionKey()
	if liveFolder == "" {
		t.Fatal("batch_pick descriptor must carry a non-empty partition_key naming the picked folder")
	}

	if _, err := srv.Commit(ctx, &genv1.CommitRequest{
		ClaimId:    "rimsky-sub-1",
		ClaimScope: staleLease.ClaimScopeData,
		LeaseToken: staleLease.GetLeaseToken(),
	}); err != nil {
		t.Fatalf("stale Commit must be a lease no-op, not an error: %v", err)
	}
	if got := subEntries(liveFolder); got != 1 {
		t.Fatalf("stale-token Commit clobbered the successor lease: %s in_progress entries = %d, want 1", liveFolder, got)
	}

	if _, err := srv.Commit(ctx, &genv1.CommitRequest{
		ClaimId:    "rimsky-sub-2",
		ClaimScope: liveLease.ClaimScopeData,
		LeaseToken: liveLease.GetLeaseToken(),
	}); err != nil {
		t.Fatalf("current-token Commit: %v", err)
	}
	if got := subEntries(liveFolder); got != 0 {
		t.Fatalf("current-token Commit must resolve the lease: %s in_progress entries = %d, want 0", liveFolder, got)
	}
}
