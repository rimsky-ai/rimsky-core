// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

// TestAdminSync_ExplicitReadmits proves the operator-triggered queue
// refresh for a `sync_strategy: explicit` pick policy: after the queue
// drains (Open returns Unavailable with no auto-sync) and a new folder
// lands on disk, a POST to the admin sync route re-admits it so a
// subsequent gRPC-shaped Open returns Available with the new folder's
// address — without redeploying the store.
//
// RED today: AdminHandler registers only POST /admin/bump-to-head/,
// so POST /admin/sync/{selector} 404s and the post-"sync" Open stays
// Unavailable. AUTHSTORES-12 adds the route to make this green.
//
// Spec story S-fsstore-explicit-sync-route. Drives the real Store +
// its real AdminHandler over httptest, plus the real producer Open —
// no Docker, no stubbed component on the path under test.
func TestAdminSync_ExplicitReadmits(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
	// Seed exactly one folder so the first Open claims it and the
	// queue then drains to empty.
	must(t, os.MkdirAll(filepath.Join(root, sub, "alpha"), 0o755))

	pp := &PickPolicy{
		Root:              sub,
		OnCommit:          action.Action{Kind: action.Pop},
		OnGiveUp:          action.Action{Kind: action.Pop},
		VisibilityTimeout: time.Minute,
		SyncStrategy:      "explicit",
	}
	st, err := New(Config{Root: root, PickPolicies: map[string]*PickPolicy{"@r": pp}})
	must(t, err)

	// Initial seed: explicit strategy never auto-syncs from Open, so the
	// operator (here, the test harness standing in for deploy-time
	// priming) runs sync once to admit the seeded folder.
	must(t, st.runSync("@r", pp))

	// Open #1 claims alpha. With on_commit: pop the queue entry is
	// consumed and not re-admitted (explicit never re-syncs).
	o1, err := st.Open(context.Background(), "c1", "@r")
	must(t, err)
	if !o1.Available {
		t.Fatal("Open #1: expected Available (seeded folder alpha), got Unavailable")
	}
	must(t, st.Commit(context.Background(), "c1", o1.Result.ClaimScope, o1.Result.Address))

	// Open #2: queue drained, explicit strategy → no auto-sync → Unavailable.
	o2, err := st.Open(context.Background(), "c2", "@r")
	must(t, err)
	if o2.Available {
		t.Fatal("Open #2: expected Unavailable on a drained explicit queue (no auto-sync), got Available")
	}

	// Drop a NEW folder onto disk under the policy root. Explicit
	// strategy will not pick this up on its own.
	must(t, os.MkdirAll(filepath.Join(root, sub, "bravo"), 0o755))

	// POST to the admin sync route to trigger an operator-driven refresh.
	srv := httptest.NewServer(st.AdminHandler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/admin/sync/%40r", "application/json", strings.NewReader(""))
	must(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST /admin/sync/@r: got %d, want 2xx; body=%s", resp.StatusCode, string(body))
	}

	// Drain the post-sync queue over the producer interface. The
	// operator-triggered refresh must make the newly-dropped folder
	// (bravo) claimable. Under the fs-store's blessed pop semantics the
	// already-popped folder (alpha) stays on disk and is re-discovered by
	// the same sync (operators delete processed folders externally to make
	// progress — see TestPattern_StaticQueue_ExplicitRefresh /
	// TestOnDrain_SinglePass), so we drain through any re-admitted folders
	// and assert that bravo, the new work, is reachable with the right
	// on-disk address. The story's value claim is "the new folder becomes
	// claimable without redeploy," which this proves; the spec does not
	// require excluding the re-discovered popped folder.
	wantAddr := filepath.Join(root, sub, "bravo")
	sawBravo := false
	for i := 0; i < 8; i++ {
		o, err := st.Open(context.Background(), fmt.Sprintf("c-drain-%d", i), "@r")
		must(t, err)
		if !o.Available {
			break
		}
		var picked struct {
			Folder string `json:"folder"`
		}
		must(t, json.Unmarshal(o.Result.Payload, &picked))
		var addr string
		must(t, json.Unmarshal(o.Result.Address, &addr))
		if picked.Folder == "bravo" {
			sawBravo = true
			if addr != wantAddr {
				t.Errorf("admin sync: bravo address %q does not correspond to the new folder %q", addr, wantAddr)
			}
		}
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-drain-%d", i), o.Result.ClaimScope, o.Result.Address))
	}
	if !sawBravo {
		t.Fatal("expected the newly-dropped folder bravo to become claimable after admin sync, but it never appeared")
	}
}
