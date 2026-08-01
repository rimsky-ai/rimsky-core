// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

type fakeControlAPIMessages struct {
	mu       sync.Mutex
	messages map[string][]map[string]any
}

func startFakeControlAPIMessages(t *testing.T) (endpoint string, teardown func()) {
	t.Helper()
	f := &fakeControlAPIMessages{messages: map[string][]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/instances/", func(w http.ResponseWriter, req *http.Request) {
		parts := splitNonEmpty(req.URL.Path, '/')
		if len(parts) < 4 || parts[0] != "v1" || parts[1] != "instances" || parts[3] != "messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		instanceID := parts[2]
		switch req.Method {
		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(req.Body).Decode(&body)
			_ = req.Body.Close()
			f.mu.Lock()
			f.messages[instanceID] = append(f.messages[instanceID], map[string]any{
				"id":          "m-" + instanceID,
				"instance_id": instanceID,
				"type":        body["type"],
			})
			f.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			f.mu.Lock()
			msgs := append([]map[string]any(nil), f.messages[instanceID]...)
			f.mu.Unlock()
			if typeFilter := req.URL.Query().Get("type"); typeFilter != "" {
				filtered := msgs[:0]
				for _, m := range msgs {
					if m["type"] == typeFilter {
						filtered = append(filtered, m)
					}
				}
				msgs = filtered
			}
			body, _ := json.Marshal(map[string]any{"messages": msgs, "next_cursor": ""})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(lis) }()
	return "http://" + lis.Addr().String(), func() { _ = srv.Close() }
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()
	_ = w.Close()
	return <-done
}

func TestRunConformancePublisher_MessagePushReachableViaControlAPIPoll(t *testing.T) {
	controlAPIURL, stopControlAPI := startFakeControlAPIMessages(t)
	t.Cleanup(stopControlAPI)

	fixture := newFixturePublisher(controlAPIURL)
	pubEndpoint, stopPub := startPublisherServer(t, fixture)
	t.Cleanup(stopPub)

	var code int
	out := captureStdout(t, func() {
		code = runConformancePublisher([]string{
			"--endpoint", "grpc://" + pubEndpoint,
			"--kind", "cron",
			"--resolved-config", `{"cron":"* * * * *"}`,
			"--instance-id", "shipped-cli-instance",
			"--control-api", controlAPIURL,
			"--timeout", "5s",
		})
	})
	if code != 0 {
		t.Fatalf("runConformancePublisher exit = %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out, "ok    MessagePush") {
		t.Fatalf("MessagePush check did not run through the shipped CLI path; output:\n%s", out)
	}
}
