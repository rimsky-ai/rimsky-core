// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/executors/verifier-http/errorclasses"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

type fakeStream struct{ events []*genv1.ExecuteEvent }

func (f *fakeStream) send(ev *genv1.ExecuteEvent) error {
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStream) terminal() *genv1.StreamClose {
	for _, ev := range f.events {
		if sc := ev.GetStreamClose(); sc != nil {
			return sc
		}
	}
	return nil
}

func buildReq(t *testing.T, ud map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	st, err := structpb.NewStruct(ud)
	if err != nil {
		t.Fatal(err)
	}
	return &genv1.ExecuteRequest{NodeType: "verifier-http", Attributes: st}
}

func TestExecuteCore_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) == 0 {
			t.Errorf("expected body")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url":             srv.URL,
		"body":            map[string]any{"k": "v"},
		"expected_status": []any{float64(200)},
	})
	fs := &fakeStream{}
	if err := executor.executeCore(context.Background(), req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil || term.GetSuccess() == nil {
		t.Fatalf("expected Success, got: %+v", term)
	}
}

func TestExecuteCore_StatusMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()
	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url":             srv.URL,
		"expected_status": []any{float64(200)},
	})
	fs := &fakeStream{}
	if err := executor.executeCore(context.Background(), req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal")
	}
	errOut := term.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %+v", term)
	}
	if errOut.GetErrorClass() != "verifier/check_failed" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

// TestExecuteCore_TimeoutClassifiesAsTimeout drives a deliberately-slow
// upstream past the configured timeout and asserts the emission
// carries the hierarchical `verifier/timeout` class (not the broader
// `verifier/network_error`). Mirrors http-node's classifyTransportErr
// discipline.
func TestExecuteCore_TimeoutClassifiesAsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url":        srv.URL,
		"timeout_ms": float64(20),
	})
	fs := &fakeStream{}
	if err := executor.executeCore(context.Background(), req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal")
	}
	errOut := term.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %+v", term)
	}
	if errOut.GetErrorClass() != "verifier/timeout" {
		t.Errorf("error_class: got %q, want verifier/timeout", errOut.GetErrorClass())
	}
}

// TestCapabilities_AdvertisesHierarchicalErrorClasses confirms the
// observability surface advertises the canonical verifier/* leaves
// imported from `pkg:executors/verifier-http/errorclasses`.
func TestCapabilities_AdvertisesHierarchicalErrorClasses(t *testing.T) {
	obs := NewObservabilityServer()
	caps, err := obs.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	declared := caps.GetDeclaredErrorClasses()
	want := errorclasses.Declared()
	if len(declared) != len(want) {
		t.Fatalf("declared_error_classes: got %v, want %v", declared, want)
	}
	for i, c := range declared {
		if c != want[i] {
			t.Errorf("declared[%d]: got %q, want %q", i, c, want[i])
		}
		if c == "" {
			t.Errorf("declared[%d]: empty string", i)
		}
	}
}

func TestExecuteCore_MissingURL(t *testing.T) {
	executor := NewServer(false)
	req := buildReq(t, map[string]any{})
	fs := &fakeStream{}
	if err := executor.executeCore(context.Background(), req, fs.send); err != nil {
		t.Fatal(err)
	}
	term := fs.terminal()
	if term == nil {
		t.Fatal("no terminal")
	}
	if term.GetError().GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: %s", term.GetError().GetErrorClass())
	}
}
