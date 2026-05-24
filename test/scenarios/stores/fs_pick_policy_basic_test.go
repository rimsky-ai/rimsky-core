// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Basic ring-cycle scenario through the fs store's pick-policy
// dispatch, exercised over the gRPC wire surface. Three folders
// auto-discover under the configured sub-root; three sequential
// Open → Commit cycles must rotate through all three (recycle).
package stores

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
	"github.com/fallguy/rimsky/stores/common/action"
	fsstore "github.com/fallguy/rimsky/stores/filesystem/store"
	fsfixture "github.com/fallguy/rimsky/stores/filesystem/testfixture"
)

// TestFsPickPolicy_BasicRingCycle verifies a full ring cycle through
// the gRPC wire surface: pick → commit (recycle) → pick
// (different folder) → commit → pick (third folder).
func TestFsPickPolicy_BasicRingCycle(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(root, sub, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pp := &fsstore.PickPolicy{
		Root: sub, OnCommit: action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
	}
	grpcAddr, _, teardown := fsfixture.Start(t, fsfixture.Config{
		Root:         root,
		PickPolicies: map[string]*fsstore.PickPolicy{"@r": pp},
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
