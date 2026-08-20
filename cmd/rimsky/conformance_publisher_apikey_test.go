// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/publisher"
)

type bearerDemandingControlAPI struct {
	required string

	mu           sync.Mutex
	seen         []string
	unauthorized int
}

func (s *bearerDemandingControlAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	s.seen = append(s.seen, presented)
	refused := presented != s.required
	if refused {
		s.unauthorized++
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if refused {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	_, _ = w.Write([]byte(`{"messages":[{"id":"msg-1","type":"system/conformance"}]}`))
}

func (s *bearerDemandingControlAPI) observed() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...), s.unauthorized
}

// @concept: rimsky
// @concept: api-key
func TestConformancePublisherPollSendsTheResolvedKey(t *testing.T) {
	const key = "rk_conformance_operator"
	stub := &bearerDemandingControlAPI{required: key}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	receiver := publisher.NewMessageReceiver()
	done := make(chan struct{})
	go func() {
		defer close(done)
		pollForConformancePublisherMessage(context.Background(), srv.URL, key, "inst-1", "system/conformance", receiver)
	}()
	<-done

	bearers, unauthorized := stub.observed()
	if len(bearers) == 0 {
		t.Fatalf("the poll sent the control API no request, so this check proved nothing")
	}
	if unauthorized != 0 {
		t.Errorf("the control API refused %d poll request(s) as unauthorized", unauthorized)
	}
	for i, presented := range bearers {
		if presented != key {
			t.Errorf("poll request %d carried bearer %q, want %q", i, presented, key)
		}
	}
}
