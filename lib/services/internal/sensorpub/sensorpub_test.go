// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sensorpub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type nopLogger struct{}

func (nopLogger) Warn(string, ...any) {}

func TestPostMessage_TrailingSlashEndpointProducesSingleSlashURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := PostMessage(context.Background(), srv.Client(), nopLogger{}, srv.URL+"/",
		"sensor-http", "poll_result", "inst-1", "sub-1", map[string]any{"a": 1}, "idem-1")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	want := "/v1/instances/inst-1/messages"
	if gotPath != want {
		t.Fatalf("path = %q, want %q (double-slash regression)", gotPath, want)
	}
}

func TestPostMessage_NoTrailingSlashEndpointStillProducesSingleSlashURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := PostMessage(context.Background(), srv.Client(), nopLogger{}, srv.URL,
		"sensor-cron", "poll_result", "inst-1", "sub-1", map[string]any{"a": 1}, "idem-1")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	want := "/v1/instances/inst-1/messages"
	if gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

func TestPostMessage_EscapesInstanceID(t *testing.T) {
	var gotEscapedPath, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := PostMessage(context.Background(), srv.Client(), nopLogger{}, srv.URL,
		"sensor-object-store", "poll_result", "inst with space", "sub-1", map[string]any{}, "")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if !strings.Contains(gotEscapedPath, "inst%20with%20space") {
		t.Fatalf("escaped path = %q, want the instance ID space-escaped", gotEscapedPath)
	}
	want := "/v1/instances/inst with space/messages"
	if gotPath != want {
		t.Fatalf("decoded path = %q, want %q", gotPath, want)
	}
}
