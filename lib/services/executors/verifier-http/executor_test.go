// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifierhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestExecute_NonNumericExpectedStatusEntry_RejectedAsAttributeInvalid(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	executor := NewServer(false)
	req := buildReq(t, map[string]any{
		"url":             ts.URL,
		"expected_status": []any{"200"},
	})
	outcome, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got: %T", outcome.GetOutcome())
	}
	if errOut.GetErrorClass() != "verifier/attribute_invalid" {
		t.Errorf("error_class: %s, want verifier/attribute_invalid", errOut.GetErrorClass())
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

func TestExecute_DefaultExpectedStatusAcceptsNon200TwoXX(t *testing.T) {
	for _, code := range []int{201, 202, 204} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		req := buildReq(t, map[string]any{"url": srv.URL})
		outcome, err := NewServer(false).Execute(context.Background(), req)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		success := outcome.GetSuccess()
		if success == nil {
			t.Fatalf("status %d: expected Success under the default 2xx-to-success mapping, got %T", code, outcome.GetOutcome())
		}
	}
}

func TestExecute_DefaultExpectedStatusRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	req := buildReq(t, map[string]any{"url": srv.URL})
	outcome, err := NewServer(false).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	payload := errOut.GetPayload().AsMap()
	if payload["expected_status"] != "2xx" {
		t.Errorf("expected_status = %+v, want \"2xx\"", payload["expected_status"])
	}
}

func TestExecute_ErrorPayloadSurvivesBinaryBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte{0xff, 0xfe, 0x00, 0x01, 0x02})
	}))
	defer srv.Close()
	req := buildReq(t, map[string]any{"url": srv.URL})
	outcome, err := NewServer(false).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errOut.GetPayload() == nil {
		t.Fatal("a non-UTF-8 response body must not drop the Error payload")
	}
	payload := errOut.GetPayload().AsMap()
	if payload["actual_status"] != float64(http.StatusInternalServerError) {
		t.Errorf("actual_status missing from payload: %+v", payload)
	}
	if payload["expected_status"] != "2xx" {
		t.Errorf("expected_status missing from payload: %+v", payload)
	}
}

func TestExecute_ErrorPayloadSurvivesMidRuneTruncation(t *testing.T) {
	body := strings.Repeat("é", 300)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	req := buildReq(t, map[string]any{"url": srv.URL})
	outcome, err := NewServer(false).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	errOut := outcome.GetError()
	if errOut == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errOut.GetPayload() == nil {
		t.Fatal("a mid-rune 512-byte truncation must not drop the Error payload")
	}
	payload := errOut.GetPayload().AsMap()
	if payload["actual_status"] != float64(http.StatusInternalServerError) {
		t.Errorf("actual_status missing from payload: %+v", payload)
	}
	preview, _ := payload["body_preview"].(string)
	if !utf8.ValidString(preview) {
		t.Errorf("body_preview is not valid UTF-8: %q", preview)
	}
}

func TestNewServer_ClientCarriesNoFixedTimeout(t *testing.T) {
	s := NewServer(false)
	if s.client.Timeout != 0 {
		t.Fatalf("client.Timeout = %v, want 0 (unbounded) so attributes.timeout_ms alone governs the per-dispatch deadline via context, not a hardcoded client cap", s.client.Timeout)
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
