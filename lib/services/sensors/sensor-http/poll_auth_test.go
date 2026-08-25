// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func subscribePoll(t *testing.T, s *SensorService, cfg map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode resolved_config: %v", err)
	}
	_, subErr := s.Subscribe(context.Background(), &genv1.SubscribeRequest{
		PublisherSubscriptionId: "w1", InstanceId: "i1", Kind: "http", ResolvedConfig: raw,
	})
	return subErr
}

func pollOnceRecordingHeaders(t *testing.T, cfg func(upstreamURL string) map[string]any) http.Header {
	t.Helper()
	var mu sync.Mutex
	var seen http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}))
	defer upstream.Close()

	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	s := NewSensorService(rimsky.URL, loopbackGuard(t), noopLogger{})
	s.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if err := subscribePoll(t, s, cfg(upstream.URL)); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	s.Tick(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if seen == nil {
		t.Fatal("the sensor never reached the upstream")
	}
	return seen
}

// @decision: http-poll-sensor-auth-outbound
func TestPollSendsTheConfiguredSecretHeaderOnEveryPoll(t *testing.T) {
	seen := pollOnceRecordingHeaders(t, func(url string) map[string]any {
		return map[string]any{
			"url":           url,
			"poll_interval": "10s",
			"auth": map[string]any{
				"mode":   "secret_header",
				"header": "X-Upstream-Token",
				"secret": "s3cret",
			},
		}
	})
	if got := seen.Get("X-Upstream-Token"); got != "s3cret" {
		t.Fatalf("poll sent X-Upstream-Token = %q, want %q", got, "s3cret")
	}
}

// @decision: http-poll-sensor-auth-outbound
func TestPollWithNoAuthBlockSendsNoCredentials(t *testing.T) {
	seen := pollOnceRecordingHeaders(t, func(url string) map[string]any {
		return map[string]any{"url": url, "poll_interval": "10s"}
	})
	if got := seen.Get("Authorization"); got != "" {
		t.Fatalf("poll sent Authorization = %q with no auth block", got)
	}
	for name := range seen {
		if strings.HasPrefix(strings.ToLower(name), "x-") {
			t.Fatalf("poll sent an unexpected credential header %q with no auth block", name)
		}
	}
}

// @decision: http-poll-sensor-auth-outbound
func TestPollWithModeNoneSendsNoCredentials(t *testing.T) {
	seen := pollOnceRecordingHeaders(t, func(url string) map[string]any {
		return map[string]any{
			"url":           url,
			"poll_interval": "10s",
			"auth":          map[string]any{"mode": "none"},
		}
	})
	for name := range seen {
		if strings.HasPrefix(strings.ToLower(name), "x-") {
			t.Fatalf("the poll sent a credential header %q under mode none", name)
		}
	}
}

// @decision: http-poll-sensor-auth-outbound
func TestSubscribeRefusesAnAuthModeThePollCannotApply(t *testing.T) {
	cases := map[string]map[string]any{
		"hmac, which signs a body a poll does not have": {
			"mode":             "hmac",
			"secret":           "s3cret",
			"timestamp_header": "X-Timestamp",
		},
		"a mode neither sensor knows":  {"mode": "oauth2"},
		"an auth block naming no mode": {"header": "X-Token", "secret": "s3cret"},
		"secret_header with no header name": {
			"mode":   "secret_header",
			"secret": "s3cret",
		},
		"secret_header with no secret": {
			"mode":   "secret_header",
			"header": "X-Token",
		},
	}
	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			s := NewSensorService("", loopbackGuard(t), noopLogger{})
			err := subscribePoll(t, s, map[string]any{
				"url":           "http://example.test/feed.json",
				"poll_interval": "10s",
				"auth":          auth,
			})
			if err == nil {
				t.Fatalf("the sensor bound a subscription whose auth block it cannot apply (%s)", name)
			}
			s.mu.Lock()
			_, mounted := s.watches["w1"]
			s.mu.Unlock()
			if mounted {
				t.Fatalf("the sensor left a refused subscription mounted (%s)", name)
			}
		})
	}
}
