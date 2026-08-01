// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostCallbackVia_RetriesUntilSuccess(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	PostCallbackVia(srv.Client())(srv.URL, map[string]any{"success": map[string]any{"changed": true}}, slog.Default())

	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3 (2 rejections then a success)", got)
	}
}

func TestPostCallbackVia_GivesUpAfterMaxAttempts(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	PostCallbackVia(srv.Client())(srv.URL, map[string]any{"success": map[string]any{"changed": true}}, slog.Default())

	if got := attempts.Load(); got != callbackPostMaxAttempts {
		t.Fatalf("attempts = %d, want %d", got, callbackPostMaxAttempts)
	}
}

func TestDefaultCallbackHTTPClientHasBoundedTimeout(t *testing.T) {
	if defaultCallbackHTTPClient.Timeout != callbackPostTimeout {
		t.Fatalf("DefaultPostCallback must use a client with a bounded per-attempt Timeout (not http.DefaultClient, which has none) so a hung supervisor cannot block the dispatch goroutine forever; got Timeout = %v, want %v",
			defaultCallbackHTTPClient.Timeout, callbackPostTimeout)
	}
}

func TestPostCallbackVia_ClientTimeoutBoundsHungServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	client := &http.Client{Timeout: 20 * time.Millisecond}
	done := make(chan struct{})
	go func() {
		PostCallbackVia(client)(srv.URL, map[string]any{"success": map[string]any{"changed": true}}, slog.Default())
		close(done)
	}()
	<-done
}
