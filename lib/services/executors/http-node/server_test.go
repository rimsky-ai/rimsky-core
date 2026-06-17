// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// server_test.go — http-node executor coverage under the unary RPC
// shape (TD-execute-rpc-unary). Each test calls Execute(req) directly
// against an httptest.NewServer upstream and asserts on the settling
// Outcome — Success/Error/Park — including attributes_delta, tags,
// and error_class propagation. The HTTP bridge tests round-trip
// through the protojson bridge mounted by mountBridge().

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// newRequest builds an ExecuteRequest with the http-node config attributes.
func newRequest(t *testing.T, ud map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	return newRequestWithAttrs(t, ud, nil)
}

// newRequestWithAttrs builds an ExecuteRequest with the merged attribute
// bag. Under the userdata collapse, callers pass config (url, method,
// body, ...) as `ud` and resolved per-run inputs as `attrs`; the helper
// merges with `attrs` overriding `ud` on collisions.
func newRequestWithAttrs(t *testing.T, ud, attrs map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	merged := map[string]any{}
	for k, v := range ud {
		merged[k] = v
	}
	for k, v := range attrs {
		merged[k] = v
	}
	st, err := structpb.NewStruct(merged)
	if err != nil {
		t.Fatalf("structpb attributes: %v", err)
	}
	return &genv1.ExecuteRequest{NodeType: "http.request@1", Attributes: st}
}

func testServer(t *testing.T, stub bool) *Server {
	t.Helper()
	return NewServer(Config{
		Host:         "127.0.0.1",
		GRPCPort:     0,
		HTTPPort:     0,
		TimeoutMs:    5000,
		MaxBodyBytes: 1 << 20,
		StubMode:     stub,
	})
}

func TestExecute_HappyPath_200JSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"name":"alice"}`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequest(t, map[string]any{"url": ts.URL, "method": "GET"})
	outcome, err := s.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success, got %T", outcome.GetOutcome())
	}
	if !success.GetChanged() {
		t.Error("expected changed=true")
	}
	got := success.GetAttributesDelta().AsMap()
	if got["ok"] != true || got["name"] != "alice" {
		t.Errorf("unexpected attributes_delta: %+v", got)
	}
}

func TestExecute_404_ReturnsExpectationMismatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequest(t, map[string]any{"url": ts.URL})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error terminal, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/expectation_mismatch" {
		t.Errorf("error_class=%q, want http/expectation_mismatch", errd.GetErrorClass())
	}
}

func TestExecute_5xx_ReturnsServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"err":"boom"}`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequest(t, map[string]any{"url": ts.URL})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/server_error/500" {
		t.Errorf("error_class=%q, want http/server_error/500", errd.GetErrorClass())
	}
}

func TestExecute_NetworkError_ReturnsTransportErr(t *testing.T) {
	s := testServer(t, false)
	// @deliberate: port 1 is reserved and unbound, so the dial fails synchronously.
	req := newRequest(t, map[string]any{"url": "http://127.0.0.1:1/does-not-exist"})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error terminal, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/network_error" {
		t.Errorf("error_class=%q, want http/network_error", errd.GetErrorClass())
	}
}

func TestExecute_Timeout_ReturnsTimeout(t *testing.T) {
	// @deliberate: upstream sleeps past the executor TimeoutMs so the client times out.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := NewServer(Config{
		Host: "127.0.0.1", TimeoutMs: 50, MaxBodyBytes: 1 << 20,
	})
	req := newRequest(t, map[string]any{"url": ts.URL})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error terminal, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/timeout" {
		t.Errorf("error_class=%q, want http/timeout", errd.GetErrorClass())
	}
}

func TestExecute_MalformedAttributes_MissingURL(t *testing.T) {
	s := testServer(t, false)
	req := newRequest(t, map[string]any{"method": "GET"})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/attribute_invalid" {
		t.Errorf("error_class=%q, want http/attribute_invalid", errd.GetErrorClass())
	}
}

func TestStubMode_ReturnsCannedResponse(t *testing.T) {
	s := testServer(t, true)
	// @constraint: stub mode must not dial — an unreachable URL still resolves to Success.
	req := newRequest(t, map[string]any{"url": "http://unreachable.invalid/"})
	outcome, err := s.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success, got %T", outcome.GetOutcome())
	}
	got := success.GetAttributesDelta().AsMap()
	if got["stub"] != true {
		t.Errorf("expected stub:true, got %+v", got)
	}
	if success.GetChangeSummary() != "stub" {
		t.Errorf("change_summary=%q, want stub", success.GetChangeSummary())
	}
}

func TestStubMode_RejectsMalformedAttributes(t *testing.T) {
	s := testServer(t, true)
	req := newRequest(t, map[string]any{})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/attribute_invalid" {
		t.Errorf("error_class=%q, want http/attribute_invalid", errd.GetErrorClass())
	}
}

func TestStubMode_WithCustomStubResponse(t *testing.T) {
	s := testServer(t, true)
	req := newRequest(t, map[string]any{
		"url": "http://unreachable.invalid/",
		"stub_response": map[string]any{
			"id":   "abc",
			"done": true,
		},
	})
	outcome, err := s.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := outcome.GetSuccess().GetAttributesDelta().AsMap()
	if got["id"] != "abc" || got["done"] != true {
		t.Errorf("custom stub_response not returned: %+v", got)
	}
}

func TestExecute_WithCustomExpectStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"brew":"done"}`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequest(t, map[string]any{
		"url":           ts.URL,
		"expect_status": []any{float64(418)},
	})
	outcome, err := s.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success, got %T", outcome.GetOutcome())
	}
	got := success.GetAttributesDelta().AsMap()
	if got["brew"] != "done" {
		t.Errorf("attributes_delta: %+v", got)
	}
}

// TestExecute_HTTPBridge_PostExecuteRoundTrip verifies the protojson
// bridge accepts an ExecuteRequest body and returns a single Outcome
// (TD-execute-rpc-unary; no streaming).
func TestExecute_HTTPBridge_PostExecuteRoundTrip(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer upstream.Close()

	s := testServer(t, false)
	mux := http.NewServeMux()
	mountBridge(mux, s)
	bridge := httptest.NewServer(mux)
	defer bridge.Close()

	ud, err := structpb.NewStruct(map[string]any{"url": upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	reqProto := &genv1.ExecuteRequest{NodeType: "http.request@1", Attributes: ud}
	body, err := protojson.Marshal(reqProto)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", bridge.URL+"/v1/Execute", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	respBody, _ := io.ReadAll(resp.Body)
	var outcome genv1.Outcome
	if err := protojson.Unmarshal(respBody, &outcome); err != nil {
		t.Fatalf("unmarshal outcome: %v", err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Success terminal, got %T", outcome.GetOutcome())
	}
	got := success.GetAttributesDelta().AsMap()
	if got["hello"] != "world" {
		t.Errorf("unexpected attributes_delta: %+v", got)
	}
}

// TestExecute_NonJSONResponse_Base64 verifies the fallback path where the
// upstream returns a non-JSON Content-Type.
func TestExecute_NonJSONResponse_Base64(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hi"))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequest(t, map[string]any{"url": ts.URL})
	outcome, err := s.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := outcome.GetSuccess().GetAttributesDelta().AsMap()
	if _, ok := got["body_base64"]; !ok {
		t.Errorf("expected body_base64, got %+v", got)
	}
	if got["content_type"] != "text/plain" {
		t.Errorf("content_type=%v", got["content_type"])
	}
}

// TestExecute_JSONContentType_InvalidBody_ReturnsParseFailed covers the
// http/response_unparseable error class.
func TestExecute_JSONContentType_InvalidBody_ReturnsParseFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json{`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequest(t, map[string]any{"url": ts.URL})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/response_unparseable" {
		t.Errorf("error_class=%q, want http/response_unparseable", errd.GetErrorClass())
	}
}

// TestExecute_PostWithStructBody verifies that attributes.body wins over
// the implicit-attributes body and is sent verbatim to the upstream.
func TestExecute_PostWithStructBody(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequestWithAttrs(t,
		map[string]any{
			"url":    ts.URL,
			"method": "POST",
			"body":   map[string]any{"name": "bob"},
		},
		// @constraint: per-run attributes carry sentinel values the upstream must NOT see.
		map[string]any{"name": "ignored", "extra": "ignored"},
	)
	if _, err := s.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got["name"] != "bob" {
		t.Errorf("upstream got: %+v", got)
	}
	if _, ok := got["extra"]; ok {
		t.Errorf("attributes leaked when attributes.body overrides: %+v", got)
	}
}

// TestExecute_AttributesAsRequestBody verifies http-node POSTs the per-run
// `attributes` map as the JSON request body when no body override is set.
func TestExecute_AttributesAsRequestBody(t *testing.T) {
	var got map[string]any
	var gotCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"upstream_ack":true}`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequestWithAttrs(t,
		map[string]any{"url": ts.URL, "method": "POST"},
		map[string]any{"customer_id": "cust_42", "topic": "alpha"},
	)
	outcome, err := s.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got["customer_id"] != "cust_42" || got["topic"] != "alpha" {
		t.Errorf("attributes were not posted as request body: %+v", got)
	}
	if !strings.Contains(gotCT, "json") {
		t.Errorf("expected json Content-Type, got %q", gotCT)
	}
	delta := outcome.GetSuccess().GetAttributesDelta().AsMap()
	if delta["upstream_ack"] != true {
		t.Errorf("expected attributes_delta to mirror upstream JSON body, got %+v", delta)
	}
}

// TestExecute_NoAttributesNoBody verifies that with neither body nor
// attributes set, the upstream receives an empty body.
func TestExecute_NoAttributesNoBody(t *testing.T) {
	var bodyLen int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		bodyLen = len(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequest(t, map[string]any{"url": ts.URL, "method": "POST"})
	if _, err := s.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if bodyLen != 0 {
		t.Errorf("expected empty upstream body, got %d bytes", bodyLen)
	}
}

// TestExecute_NonObjectJSONResponse_ReturnsParseFailed verifies that JSON
// arrays / scalars from the upstream cannot become attributes_delta.
func TestExecute_NonObjectJSONResponse_ReturnsParseFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[1,2,3]`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	req := newRequest(t, map[string]any{"url": ts.URL})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/response_unparseable" {
		t.Errorf("error_class=%q, want http/response_unparseable", errd.GetErrorClass())
	}
}

// TestHttpNode_429ParksWithResumeAtAndAutoWakes asserts that an upstream
// 429 with Retry-After resolves to Park{SNOOZE, resume_at}, not Error.
// A subsequent re-dispatch against an upstream that now returns 200
// reaches Success.
func TestHttpNode_429ParksWithResumeAtAndAutoWakes(t *testing.T) {
	const retryAfterSeconds = 7

	var mu sync.Mutex
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"detail":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := testServer(t, false)

	req1 := newRequest(t, map[string]any{"url": ts.URL, "method": "GET"})
	before := time.Now()
	outcome1, err := s.Execute(context.Background(), req1)
	if err != nil {
		t.Fatalf("Execute (first dispatch): %v", err)
	}
	after := time.Now()

	if errd := outcome1.GetError(); errd != nil {
		t.Fatalf("429 must Park, not Error; got error_class=%q", errd.GetErrorClass())
	}
	park := outcome1.GetPark()
	if park == nil {
		t.Fatalf("expected Park on 429, got %T", outcome1.GetOutcome())
	}
	if park.GetReason() != genv1.ParkReason_PARK_REASON_SNOOZE {
		t.Errorf("park reason=%v, want PARK_REASON_SNOOZE", park.GetReason())
	}
	if park.GetResumeAt() == nil {
		t.Fatalf("expected Park.ResumeAt computed from Retry-After, got nil")
	}
	resumeAt := park.GetResumeAt().AsTime()
	lo := before.Add(retryAfterSeconds*time.Second - 2*time.Second)
	hi := after.Add(retryAfterSeconds*time.Second + 2*time.Second)
	if resumeAt.Before(lo) || resumeAt.After(hi) {
		t.Errorf("resume_at=%v out of expected window [%v, %v]", resumeAt, lo, hi)
	}

	req2 := newRequest(t, map[string]any{"url": ts.URL, "method": "GET"})
	outcome2, err := s.Execute(context.Background(), req2)
	if err != nil {
		t.Fatalf("Execute (re-dispatch): %v", err)
	}
	resumed := outcome2.GetSuccess()
	if resumed == nil {
		t.Fatalf("expected Success on auto-wake re-dispatch, got %T", outcome2.GetOutcome())
	}
	if resumed.GetAttributesDelta().AsMap()["ok"] != true {
		t.Errorf("attributes_delta did not mirror upstream: %+v",
			resumed.GetAttributesDelta().AsMap())
	}
}

// TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback covers
// the configurable upstream error-class field and the /_unspecified
// fallback for absent fields.
func TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback(t *testing.T) {
	t.Run("ConfiguredFieldRead", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"quota_exhausted","message":"over quota"}`))
		}))
		defer ts.Close()

		s := testServer(t, false)
		req := newRequest(t, map[string]any{
			"url":               ts.URL,
			"method":            "GET",
			"error_class_field": "code",
		})
		outcome, _ := s.Execute(context.Background(), req)
		errd := outcome.GetError()
		if errd == nil {
			t.Fatalf("expected Error terminal, got %T", outcome.GetOutcome())
		}
		if got, want := errd.GetErrorClass(), "http/request_invalid/quota_exhausted"; got != want {
			t.Errorf("error_class=%q, want %q", got, want)
		}
	})

	t.Run("AbsentFieldUnspecifiedFallback", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"bad request","detail":"missing param"}`))
		}))
		defer ts.Close()

		s := testServer(t, false)
		req := newRequest(t, map[string]any{"url": ts.URL, "method": "GET"})
		outcome, _ := s.Execute(context.Background(), req)
		errd := outcome.GetError()
		if errd == nil {
			t.Fatalf("expected Error terminal, got %T", outcome.GetOutcome())
		}
		got := errd.GetErrorClass()
		if !strings.HasSuffix(got, "/_unspecified") {
			t.Errorf("error_class=%q, want a /_unspecified leaf", got)
		}
		if got == "http/expectation_mismatch" {
			t.Errorf("absent field must fall back to /_unspecified, not %q", got)
		}
	})
}

// TestStubMode_RejectsNonObjectStubResponse covers the spec constraint
// that attributes_delta is a JSON object — non-object stub_response
// values must be rejected as http/attribute_invalid.
func TestStubMode_RejectsNonObjectStubResponse(t *testing.T) {
	s := testServer(t, true)
	req := newRequest(t, map[string]any{
		"url":           "http://unreachable.invalid/",
		"stub_response": "not-an-object",
	})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %T", outcome.GetOutcome())
	}
	if errd.GetErrorClass() != "http/attribute_invalid" {
		t.Errorf("error_class=%q, want http/attribute_invalid", errd.GetErrorClass())
	}
}

// TestExecute_ProbeParkAwaitCallback covers the Park-outcome shape probe
// surfaced by the stub-mode `probe_park` escape hatch (the closed
// two-value ParkReason set). The await_callback variant flows to
// Park{AWAIT_CALLBACK} so the conformance harness's park-reason
// scenarios are unit-tested here too.
func TestExecute_ProbeParkAwaitCallback(t *testing.T) {
	s := testServer(t, true)
	req := newRequest(t, map[string]any{
		"url":         "http://stub/",
		"probe_park":  true,
		"park_reason": "await_callback",
	})
	outcome, _ := s.Execute(context.Background(), req)
	park := outcome.GetPark()
	if park == nil {
		t.Fatalf("expected Park, got %T", outcome.GetOutcome())
	}
	if park.GetReason() != genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK {
		t.Errorf("reason=%v, want PARK_REASON_AWAIT_CALLBACK", park.GetReason())
	}
}
