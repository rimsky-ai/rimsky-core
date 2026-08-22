// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

func rootedSpec() map[string]any {
	return map[string]any{
		"name":    "rooted",
		"version": "1.0",
		"nodes":   []map[string]any{{"type": "a", "executor": "http-node"}},
	}
}

func rootlessSpec() map[string]any {
	return map[string]any{
		"name":    "rootless",
		"version": "1.0",
		"nodes": []map[string]any{{
			"type":       "b",
			"executor":   "http-node",
			"subscribes": []map[string]any{{"type": "external/trigger", "force_upstream_refresh": false}},
		}},
	}
}

func wakeTestServer(t *testing.T, specByHash map[string]map[string]any, woken *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/templates/"):
			hash := strings.TrimPrefix(r.URL.Path, "/v1/templates/")
			spec, ok := specByHash[hash]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "no such template"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"template_id": hash, "spec": spec})
		case strings.HasSuffix(r.URL.Path, "/messages"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/instances/"), "/messages")
			mu.Lock()
			*woken = append(*woken, id)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"message_id": "msg-1"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// @story: one-shot-to-terminal
// @decision: compose-driver-sends-empty-message-after-create
func TestWakeCreatedInstances_NamesTheInstanceItLeavesUndriven(t *testing.T) {
	var woken []string
	var mu sync.Mutex
	srv := wakeTestServer(t, map[string]map[string]any{
		"sha256-rooted":   rootedSpec(),
		"sha256-rootless": rootlessSpec(),
	}, &woken, &mu)
	defer srv.Close()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	err := WakeCreatedInstances(context.Background(), cli.NewClient(srv.URL), []CreatedInstance{
		{Key: "driven", ID: "inst-driven", TemplateHash: "sha256-rooted"},
		{Key: "undriven", ID: "inst-undriven", TemplateHash: "sha256-rootless"},
	}, logger)
	if err != nil {
		t.Fatalf("a rootless instance in a manifest must not fail the whole run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(woken) != 1 || woken[0] != "inst-driven" {
		t.Fatalf("woken = %v, want the rooted instance alone", woken)
	}
	line := logs.String()
	if !strings.Contains(line, "compose.rootless_instance_not_woken") {
		t.Fatalf("logs = %q, want a warning naming the instance compose run leaves undriven", line)
	}
	if !strings.Contains(line, "undriven") || !strings.Contains(line, "inst-undriven") {
		t.Fatalf("logs = %q, want the warning to name the instance key and id", line)
	}
	if strings.Contains(line, "inst-driven") {
		t.Fatalf("logs = %q, want no warning for the instance compose run woke", line)
	}
}
