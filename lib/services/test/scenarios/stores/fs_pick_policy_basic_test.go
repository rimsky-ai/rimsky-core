// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Basic ring-cycle scenario through the filesystem store's pick-policy
// dispatch, exercised over the real gRPC wire surface. Three folders
// auto-discover under the configured sub-root; three sequential
// Open → Commit cycles must rotate through all three (recycle).
//
// The pre-2026-05-24-repo-reorganization version of this test imported
// `pkg:stores/filesystem/testfixture` from rimsky. Post-reorganization
// the production filesystem-store impl lives here under lib/services,
// and the rimsky-side testfixture is a stub-wrapping shim. To preserve
// the real-store wire coverage, this rewrite bootstraps the production
// `pkg:stores/filesystem/server` directly in the test process and
// dials it over loopback. No rimsky bring-up is needed — the test is
// a pure claim-producer wire exerciser.
package stores

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/stores/filesystem/server"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/filesystem/store"
)

// TestFsPickPolicy_BasicRingCycle verifies a full ring cycle through
// the gRPC wire surface: pick → commit (recycle) → pick (different
// folder) → commit → pick (third folder). Three distinct folders must
// appear across three cycles.
func TestFsPickPolicy_BasicRingCycle(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(root, sub, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pp := &fsstore.PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "on_open",
	}
	grpcAddr, teardown := startFilesystemStore(t, server.Config{
		Root:          root,
		PickPolicies:  map[string]*fsstore.PickPolicy{"@r": pp},
		SweepInterval: 60 * time.Second,
	})
	t.Cleanup(teardown)

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()
	client := genv1.NewClaimProducerClient(conn)

	picked := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		claimID := fmt.Sprintf("c-%d", i)
		o, err := client.Open(context.Background(), &genv1.OpenRequest{
			ClaimId: claimID, Selector: "@r", Intent: "rw",
		})
		if err != nil {
			t.Fatalf("Open[%d]: %v", i, err)
		}
		acq := o.GetAcquired()
		if acq == nil {
			t.Fatalf("Open[%d]: expected Acquired, got Unavailable", i)
		}
		var p struct{ Folder string }
		if err := json.Unmarshal(acq.Payload, &p); err != nil {
			t.Fatalf("payload: %v", err)
		}
		picked = append(picked, p.Folder)
		if _, err := client.Commit(context.Background(), &genv1.CommitRequest{
			ClaimId: claimID, ClaimScope: acq.ClaimScope, Address: acq.Address,
		}); err != nil {
			t.Fatalf("Commit[%d]: %v", i, err)
		}
	}
	seen := make(map[string]bool)
	for _, f := range picked {
		if seen[f] {
			t.Errorf("folder %s picked twice in 3 cycles; ring should rotate", f)
		}
		seen[f] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 unique folders picked, got %d: %v", len(seen), picked)
	}
}

// startFilesystemStore brings up the production filesystem-store
// server in-process on loopback. Returns the gRPC dial address and a
// teardown. Pre-2026-05-24 the in-rimsky `fsfixture.Start` exposed the
// same shape but against the in-rimsky store package — that package
// moved to lib/services, so this helper inlines the same start /
// listen / serve / cancel dance.
func startFilesystemStore(t *testing.T, cfg server.Config) (grpcAddr string, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("filesystem store grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("filesystem store http listen: %v", err)
	}
	var adminLis net.Listener
	if len(cfg.PickPolicies) > 0 {
		adminLis, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = grpcLis.Close()
			_ = httpLis.Close()
			t.Fatalf("filesystem store admin listen: %v", err)
		}
	}
	addr := grpcLis.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		// @deliberate: Server.Run blocks until ctx is cancelled; close `done` on
		// return so the teardown can wait for orderly shutdown.
		_ = server.Run(ctx, cfg, grpcLis, httpLis, adminLis)
		close(done)
	}()

	return addr, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

// _ guards against unused imports if a future test removes the helper.
var _ = http.StatusOK
