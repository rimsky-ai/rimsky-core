// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/server"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/store"
)

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

var _ = http.StatusOK
