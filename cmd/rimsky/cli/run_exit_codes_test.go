// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func oneShotServer(t *testing.T, terminated bool, failedCount int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/instances/"):
			body := map[string]any{"id": "inst-1"}
			if terminated {
				body["terminated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
			}
			_ = json.NewEncoder(w).Encode(body)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
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
