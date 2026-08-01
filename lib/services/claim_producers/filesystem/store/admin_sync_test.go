// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func TestAdminSync_ExplicitReadmits(t *testing.T) {
	root := t.TempDir()
	sub := "docs"
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

	must(t, st.runSync("@r", pp))

	o1, err := st.Open(context.Background(), "c1", "@r")
	must(t, err)
	if !o1.Available {
		t.Fatal("Open #1: expected Available (seeded folder alpha), got Unavailable")
	}
	must(t, st.Commit(context.Background(), "c1", o1.Result.ClaimScope, o1.Result.Address, ""))

	o2, err := st.Open(context.Background(), "c2", "@r")
	must(t, err)
	if o2.Available {
		t.Fatal("Open #2: expected Unavailable on a drained explicit queue (no auto-sync), got Available")
	}

	must(t, os.MkdirAll(filepath.Join(root, sub, "bravo"), 0o755))

	srv := httptest.NewServer(st.AdminHandler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/admin/sync/%40r", "application/json", strings.NewReader(""))
	must(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST /admin/sync/@r: got %d, want 2xx; body=%s", resp.StatusCode, string(body))
	}

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
		must(t, st.Commit(context.Background(), fmt.Sprintf("c-drain-%d", i), o.Result.ClaimScope, o.Result.Address, ""))
	}
	if !sawBravo {
		t.Fatal("expected the newly-dropped folder bravo to become claimable after admin sync, but it never appeared")
	}
}
