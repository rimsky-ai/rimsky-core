// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-http/errorclasses"
)

func buildReq(t *testing.T, ud map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	st, err := structpb.NewStruct(ud)
	if err != nil {
		t.Fatal(err)
	}
	return &genv1.ExecuteRequest{NodeType: "verifier-http", Attributes: st}
}

func TestExecute_HappyPath(t *testing.T) {
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
	outcome, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success, got: %T", outcome.GetOutcome())
	}
	if success.GetChanged() {
		t.Error("verifier success must report changed=false")
	}
	delta := success.GetAttributesDelta().AsMap()
	if delta["verifier_pass"] != true {
		t.Errorf("expected verifier_pass=true in attributes_delta, got %+v", delta)
	}
}

func TestExecute_StatusMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()
	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url":             srv.URL,
		"expected_status": []any{float64(200)},
	})
	outcome, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/check_failed" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

func TestExecute_StatusMismatchWithUpstreamClass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"class":"quota_exhausted","message":"too many"}`))
	}))
	defer srv.Close()
	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url": srv.URL,
	})
	outcome, _ := executor.Execute(context.Background(), req)
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %T", outcome.GetOutcome())
	}
	if got, want := errOut.GetErrorClass(), "verifier/check_failed/quota_exhausted"; got != want {
		t.Errorf("error_class=%q, want %q", got, want)
	}
	payload := errOut.GetPayload().AsMap()
	if payload["upstream_class"] != "quota_exhausted" {
		t.Errorf("expected upstream_class on payload, got %+v", payload)
	}
}

func TestExecute_TimeoutClassifiesAsTimeout(t *testing.T) {
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
	outcome, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/timeout" {
		t.Errorf("error_class: got %q, want verifier/timeout", errOut.GetErrorClass())
	}
}

func TestExecute_NetworkError(t *testing.T) {
	executor := NewServer(false)
	req := buildReq(t, map[string]any{"url": "http://127.0.0.1:1/nope"})
	outcome, _ := executor.Execute(context.Background(), req)
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/network_error" {
		t.Errorf("error_class: got %q, want verifier/network_error", errOut.GetErrorClass())
	}
}

func TestExecute_MissingURL(t *testing.T) {
	executor := NewServer(false)
	req := buildReq(t, map[string]any{})
	outcome, _ := executor.Execute(context.Background(), req)
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: %s", errOut.GetErrorClass())
	}
}

func TestExecute_StubMode(t *testing.T) {
	executor := NewServer(true)
	req := buildReq(t, map[string]any{"url": "http://unreachable.invalid/"})
	outcome, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success, got %T", outcome.GetOutcome())
	}
	if success.GetAttributesDelta().AsMap()["stub"] != true {
		t.Errorf("expected stub:true in delta, got %+v", success.GetAttributesDelta().AsMap())
	}
}

func TestExecute_CustomClassField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"rate_limited"}`))
	}))
	defer srv.Close()
	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url":         srv.URL,
		"class_field": "code",
	})
	outcome, _ := executor.Execute(context.Background(), req)
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if got, want := errOut.GetErrorClass(), "verifier/check_failed/rate_limited"; got != want {
		t.Errorf("error_class=%q, want %q", got, want)
	}
}

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
