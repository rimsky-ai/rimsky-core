// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

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

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/egress"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func newRequest(t *testing.T, ud map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	return newRequestWithAttrs(t, ud, nil)
}

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

func loopbackGuard(t *testing.T) egress.Guard {
	t.Helper()
	g, err := egress.NewGuard([]string{"127.0.0.0/8", "::1/128"})
	if err != nil {
		t.Fatalf("build loopback egress guard: %v", err)
	}
	return g
}

func testServer(t *testing.T, stub bool) *Server {
	t.Helper()
	return NewServer(Opts{
		Host:         "127.0.0.1",
		GRPCPort:     0,
		HTTPPort:     0,
		TimeoutMs:    5000,
		MaxBodyBytes: 1 << 20,
		StubMode:     stub,
		Egress:       loopbackGuard(t),
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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := NewServer(Opts{
		Host: "127.0.0.1", TimeoutMs: 50, MaxBodyBytes: 1 << 20,
		Egress: loopbackGuard(t),
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

func TestExecute_BlocksCloudMetadataEndpoint(t *testing.T) {
	s := testServer(t, false)
	req := newRequest(t, map[string]any{"url": "http://169.254.169.254/latest/meta-data/"})
	outcome, _ := s.Execute(context.Background(), req)
	errd := outcome.GetError()
	if errd == nil {
		t.Fatalf("expected Error terminal for blocked metadata endpoint, got %T", outcome.GetOutcome())
	}
	if outcome.GetSuccess() != nil {
		t.Fatal("metadata endpoint must not be fetched")
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

func TestExecute_HTTPBridge_PostExecuteRoundTrip(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer upstream.Close()

	s := testServer(t, false)
	mux := http.NewServeMux()
	MountBridge(mux, s)
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
	if park.GetResumeAt() == nil {
		t.Fatalf("expected Park.ResumeAt computed from Retry-After, got nil")
	}
	if tags := park.GetTags(); len(tags) != 1 || tags[0] != TagRateLimited {
		t.Errorf("park tags=%v, want [%s]", tags, TagRateLimited)
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

func TestExecute_ProbeParkHonorsRequestedResumeAt(t *testing.T) {
	s := testServer(t, true)
	resumeAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	req := newRequest(t, map[string]any{
		"url":            "http://stub/",
		"probe_park":     true,
		"park_resume_at": resumeAt.Format(time.RFC3339Nano),
	})
	outcome, _ := s.Execute(context.Background(), req)
	park := outcome.GetPark()
	if park == nil {
		t.Fatalf("expected Park, got %T", outcome.GetOutcome())
	}
	if park.GetResumeAt() == nil {
		t.Fatal("resume_at not set")
	}
	if got := park.GetResumeAt().AsTime(); !got.Equal(resumeAt) {
		t.Errorf("resume_at=%v, want %v", got, resumeAt)
	}
}
