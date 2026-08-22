// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// @decision: termination
func oneShotServer(t *testing.T, quiescent bool, failedCount int) *httptest.Server {
	t.Helper()
	var terminateCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"nodes": []map[string]any{{
					"id":          "node-1",
					"instance_id": "inst-1",
					"run_summary": map[string]any{"fresh_count": 1, "failed_count": failedCount},
				}},
			})
		case strings.HasSuffix(r.URL.Path, "/frames"):
			frames := []map[string]any{}
			if !quiescent {
				frames = append(frames, map[string]any{"frame_id": "frame-1", "instance_id": "inst-1"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"frames": frames})
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]any{}})
		case strings.HasSuffix(r.URL.Path, "/terminate"):
			terminateCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            "inst-1",
				"terminated_at": time.Now().UTC().Format(time.RFC3339Nano),
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	t.Cleanup(func() {
		if quiescent && terminateCalls.Load() == 0 {
			t.Error("the run reached a quiescent instance and never terminated it; " +
				"the verb that drove the work owns the terminate action")
		}
	})
	return srv
}

// @decision: exit-codes
// @story: script-friendly-outcome
func TestWaitAndCleanup_TerminalSuccessExitsZero(t *testing.T) {
	srv := oneShotServer(t, true, 0)
	defer srv.Close()

	got := waitAndCleanup(context.Background(), NewClient(srv.URL), "inst-1", "hash-1", time.Millisecond, 0)
	if got != ExitAllSuccess {
		t.Fatalf("waitAndCleanup over a clean terminal instance = %d, want %d", got, ExitAllSuccess)
	}
}

// @decision: exit-codes
// @story: script-friendly-outcome
func TestWaitAndCleanup_TerminalFailureExitsOne(t *testing.T) {
	srv := oneShotServer(t, true, 1)
	defer srv.Close()

	got := waitAndCleanup(context.Background(), NewClient(srv.URL), "inst-1", "hash-1", time.Millisecond, 0)
	if got != ExitAnyFailure {
		t.Fatalf("waitAndCleanup over an instance with a failed node = %d, want %d: the remote one-shot run "+
			"reads the instance's outcome to choose between success and failure", got, ExitAnyFailure)
	}
}

// @decision: exit-codes
// @story: script-friendly-outcome
func TestWaitAndCleanup_TimeoutExitsTwo(t *testing.T) {
	srv := oneShotServer(t, false, 0)
	defer srv.Close()

	got := waitAndCleanup(context.Background(), NewClient(srv.URL), "inst-1", "hash-1", time.Millisecond, time.Millisecond)
	if got != ExitTimeout {
		t.Fatalf("waitAndCleanup past its timeout = %d, want %d: the run-timeout class is distinguishable "+
			"from instance failure", got, ExitTimeout)
	}
}
