// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"bufio"
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

// collector is a tiny sendFunc collector used by every test.
type collector struct {
	mu     sync.Mutex
	events []*genv1.ExecuteEvent
}

func (c *collector) send(e *genv1.ExecuteEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *collector) terminal() *genv1.ExecuteEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return nil
	}
	return c.events[len(c.events)-1]
}

func newRequest(t *testing.T, ud map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	return newRequestWithAttrs(t, ud, nil)
}

// newRequestWithAttrs builds an ExecuteRequest with the merged attribute
// bag the http-node executor reads from. Under the 2026-05-21 userdata
// collapse, the two pre-collapse channels (userdata + attributes) merge
// into a single `attributes` field on the wire: callers pass config
// (url, method, body, ...) as `ud` and resolved per-run inputs as
// `attrs`; the helper merges with `attrs` overriding `ud` on collisions
// (matches the L4 > L1 specificity rule for attribute overrides).
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
	c := &collector{}
	req := newRequest(t, map[string]any{"url": ts.URL, "method": "GET"})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}

	term := c.terminal()
	if term == nil {
		t.Fatal("no terminal event")
	}
	cmp := term.GetStreamClose().GetSuccess()
	if cmp == nil {
		t.Fatalf("expected Success, got %T", term.GetEvent())
	}
	if !cmp.GetChanged() {
		t.Error("expected changed=true")
	}
	got := cmp.GetAttributesDelta().AsMap()
	if got["ok"] != true || got["name"] != "alice" {
		t.Errorf("unexpected attributes_delta: %+v", got)
	}
	if c.events[0].GetHeartbeat() == nil {
		t.Error("expected leading heartbeat")
	}
}

func TestExecute_404_ReturnsHTTPUnexpectedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}))
	defer ts.Close()

	s := testServer(t, false)
	c := &collector{}
	req := newRequest(t, map[string]any{"url": ts.URL})
	_ = s.executeCore(context.Background(), req, c.send)

	errd := c.terminal().GetStreamClose().GetError()
	if errd == nil {
		t.Fatalf("expected Error terminal, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http/expectation_mismatch" {
		t.Errorf("error_class=%q, want http/expectation_mismatch", errd.GetErrorClass())
	}
}

func TestExecute_NetworkError_ReturnsHTTPRequestFailed(t *testing.T) {
	s := testServer(t, false)
	c := &collector{}
	// @deliberate: port 1 is reserved and unbound, so the dial fails synchronously and exercises the network_error path.
	req := newRequest(t, map[string]any{"url": "http://127.0.0.1:1/does-not-exist"})
	_ = s.executeCore(context.Background(), req, c.send)

	errd := c.terminal().GetStreamClose().GetError()
	if errd == nil {
		t.Fatalf("expected Error terminal, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http/network_error" {
		t.Errorf("error_class=%q, want http/network_error", errd.GetErrorClass())
	}
}

func TestExecute_MalformedAttributes_MissingURL(t *testing.T) {
	s := testServer(t, false)
	c := &collector{}
	req := newRequest(t, map[string]any{"method": "GET"})
	_ = s.executeCore(context.Background(), req, c.send)

	errd := c.terminal().GetStreamClose().GetError()
	if errd == nil {
		t.Fatalf("expected Error terminal, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http/attribute_invalid" {
		t.Errorf("error_class=%q, want http/attribute_invalid", errd.GetErrorClass())
	}
}

func TestStubMode_ReturnsCannedResponse(t *testing.T) {
	s := testServer(t, true)
	c := &collector{}
	// @constraint: stub mode must not dial the URL, so an unreachable host must still resolve to Success.
	req := newRequest(t, map[string]any{"url": "http://unreachable.invalid/"})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}

	cmp := c.terminal().GetStreamClose().GetSuccess()
	if cmp == nil {
		t.Fatalf("expected Success, got %+v", c.terminal().GetEvent())
	}
	got := cmp.GetAttributesDelta().AsMap()
	if got["stub"] != true {
		t.Errorf("expected stub:true, got %+v", got)
	}
	if cmp.GetChangeSummary() != "stub" {
		t.Errorf("change_summary=%q, want stub", cmp.GetChangeSummary())
	}
}

// TestStubMode_RejectsMalformedAttributes verifies the protocol contract that
// executors validate attribute shape consistently in both stub and live modes
// (Spec §14.4 + conformance `malformed_attributes` scenario).
func TestStubMode_RejectsMalformedAttributes(t *testing.T) {
	s := testServer(t, true)
	c := &collector{}
	req := newRequest(t, map[string]any{})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	if len(c.events) < 2 {
		t.Fatalf("expected >=2 events (heartbeat + errored), got %d", len(c.events))
	}
	errd := c.terminal().GetStreamClose().GetError()
	if errd == nil {
		t.Fatalf("expected Error terminal, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http/attribute_invalid" {
		t.Errorf("error_class=%q, want http/attribute_invalid", errd.GetErrorClass())
	}
}

func TestStubMode_WithCustomStubResponse(t *testing.T) {
	s := testServer(t, true)
	c := &collector{}
	req := newRequest(t, map[string]any{
		"url": "http://unreachable.invalid/",
		"stub_response": map[string]any{
			"id":   "abc",
			"done": true,
		},
	})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	got := c.terminal().GetStreamClose().GetSuccess().GetAttributesDelta().AsMap()
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
	c := &collector{}
	req := newRequest(t, map[string]any{
		"url":           ts.URL,
		"expect_status": []any{float64(418)},
	})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	cmp := c.terminal().GetStreamClose().GetSuccess()
	if cmp == nil {
		t.Fatalf("expected Success, got %+v", c.terminal().GetEvent())
	}
	got := cmp.GetAttributesDelta().AsMap()
	if got["brew"] != "done" {
		t.Errorf("attributes_delta: %+v", got)
	}
}

func TestHTTPBridge_PostExecute_ReturnsNdjsonStream(t *testing.T) {
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
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "ndjson") {
		t.Errorf("content-type=%q, want ndjson", ct)
	}

	var events []*genv1.ExecuteEvent
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var ev genv1.ExecuteEvent
		if err := protojson.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal line %q: %v", scanner.Text(), err)
		}
		events = append(events, &ev)
	}
	if len(events) < 2 {
		t.Fatalf("expected >=2 events (heartbeat + terminal), got %d", len(events))
	}
	last := events[len(events)-1]
	if last.GetStreamClose().GetSuccess() == nil {
		t.Fatalf("expected Success terminal, got %+v", last.GetEvent())
	}
	got := last.GetStreamClose().GetSuccess().GetAttributesDelta().AsMap()
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
	c := &collector{}
	req := newRequest(t, map[string]any{"url": ts.URL})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	got := c.terminal().GetStreamClose().GetSuccess().GetAttributesDelta().AsMap()
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
	c := &collector{}
	req := newRequest(t, map[string]any{"url": ts.URL})
	_ = s.executeCore(context.Background(), req, c.send)
	errd := c.terminal().GetStreamClose().GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http/response_unparseable" {
		t.Errorf("error_class=%q, want http/response_unparseable", errd.GetErrorClass())
	}
}

// TestExecute_PostWithStructBody verifies that a attributes.body override is
// JSON-serialised and sent to the upstream verbatim, taking precedence over
// any `attributes` payload.
func TestExecute_PostWithStructBody(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	c := &collector{}
	req := newRequestWithAttrs(t,
		map[string]any{
			"url":    ts.URL,
			"method": "POST",
			"body":   map[string]any{"name": "bob"},
		},
		// @constraint: per-run attributes carry sentinel values the upstream must NOT see, proving attributes.body wins over the attributes channel.
		map[string]any{"name": "ignored", "extra": "ignored"},
	)
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	if got["name"] != "bob" {
		t.Errorf("upstream got: %+v", got)
	}
	if _, ok := got["extra"]; ok {
		t.Errorf("attributes leaked when attributes.body overrides: %+v", got)
	}
}

// TestExecute_AttributesAsRequestBody verifies the spec §5.8 contract that
// http-node POSTs the per-run `attributes` map as the JSON request body when
// no explicit attributes.body override is set.
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
	c := &collector{}
	req := newRequestWithAttrs(t,
		map[string]any{"url": ts.URL, "method": "POST"},
		map[string]any{"customer_id": "cust_42", "topic": "alpha"},
	)
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	if got["customer_id"] != "cust_42" || got["topic"] != "alpha" {
		t.Errorf("attributes were not posted as request body: %+v", got)
	}
	if !strings.Contains(gotCT, "json") {
		t.Errorf("expected json Content-Type, got %q", gotCT)
	}
	delta := c.terminal().GetStreamClose().GetSuccess().GetAttributesDelta().AsMap()
	if delta["upstream_ack"] != true {
		t.Errorf("expected attributes_delta to mirror upstream JSON body, got %+v", delta)
	}
}

// TestExecute_NoAttributesNoBody verifies that with neither attributes.body nor
// attributes set, the upstream receives an empty body (no surprise payload).
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
	c := &collector{}
	req := newRequest(t, map[string]any{"url": ts.URL, "method": "POST"})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	if bodyLen != 0 {
		t.Errorf("expected empty upstream body, got %d bytes", bodyLen)
	}
}

// TestExecute_NonObjectJSONResponse_ReturnsParseFailed verifies that JSON
// arrays / scalars from the upstream cannot become attributes_delta — that
// shape must be a JSON object per spec §12.2.
func TestExecute_NonObjectJSONResponse_ReturnsParseFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[1,2,3]`))
	}))
	defer ts.Close()

	s := testServer(t, false)
	c := &collector{}
	req := newRequest(t, map[string]any{"url": ts.URL})
	_ = s.executeCore(context.Background(), req, c.send)
	errd := c.terminal().GetStreamClose().GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http/response_unparseable" {
		t.Errorf("error_class=%q, want http/response_unparseable", errd.GetErrorClass())
	}
}

// TestHttpNode_429ParksWithResumeAtAndAutoWakes pins the
// S-executors-http-node-429-park-resume contract: when an upstream returns
// 429 with a Retry-After header, the http-node MUST resolve the dispatch with
// a StreamClose Park outcome carrying ParkReason PARK_REASON_SNOOZE and a
// resume_at computed from Retry-After (≈ now + Retry-After seconds), NOT a
// hard StreamClose Error. A subsequent re-dispatch (simulating the
// supervisor's auto-wake at resume_at) against an upstream that now returns
// 200 with a JSON object body MUST reach StreamClose Success.
//
// This reuses rimsky's existing Park-outcome + resume_at auto-wake mechanism
// (the same PARK_REASON_SNOOZE + ResumeAt shape claude-agent's rate-limit
// path already emits) rather than new park machinery; the supervisor wake
// path itself is exercised by the full-stack acceptance gate.
func TestHttpNode_429ParksWithResumeAtAndAutoWakes(t *testing.T) {
	const retryAfterSeconds = 7

	// @deliberate: first call returns 429+Retry-After, subsequent calls return 200, modeling a rate-limited upstream that recovers before the supervisor's resume_at auto-wake.
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

	c1 := &collector{}
	req1 := newRequest(t, map[string]any{"url": ts.URL, "method": "GET"})
	before := time.Now()
	if err := s.executeCore(context.Background(), req1, c1.send); err != nil {
		t.Fatalf("executeCore (first dispatch): %v", err)
	}
	after := time.Now()

	sc := c1.terminal().GetStreamClose()
	if sc == nil {
		t.Fatalf("expected a StreamClose terminal, got %+v", c1.terminal().GetEvent())
	}
	if errd := sc.GetError(); errd != nil {
		t.Fatalf("429 must Park, not Error; got error_class=%q", errd.GetErrorClass())
	}
	park := sc.GetPark()
	if park == nil {
		t.Fatalf("expected StreamClose Park on 429, got %+v", c1.terminal().GetEvent())
	}
	if park.GetReason() != genv1.ParkReason_PARK_REASON_SNOOZE {
		t.Errorf("park reason=%v, want PARK_REASON_SNOOZE", park.GetReason())
	}
	if park.GetResumeAt() == nil {
		t.Fatalf("expected Park.ResumeAt computed from Retry-After, got nil")
	}
	resumeAt := park.GetResumeAt().AsTime()
	// @deliberate: tolerance window is bounded by before/after wall-clock readings straddling the dispatch, with a 2s slack on each side for scheduler jitter.
	lo := before.Add(retryAfterSeconds*time.Second - 2*time.Second)
	hi := after.Add(retryAfterSeconds*time.Second + 2*time.Second)
	if resumeAt.Before(lo) || resumeAt.After(hi) {
		t.Errorf("resume_at=%v out of expected window [%v, %v] (now + %ds)", resumeAt, lo, hi, retryAfterSeconds)
	}

	c2 := &collector{}
	req2 := newRequest(t, map[string]any{"url": ts.URL, "method": "GET"})
	if err := s.executeCore(context.Background(), req2, c2.send); err != nil {
		t.Fatalf("executeCore (re-dispatch): %v", err)
	}
	resumed := c2.terminal().GetStreamClose()
	if resumed.GetSuccess() == nil {
		t.Fatalf("expected Success on auto-wake re-dispatch, got %+v", c2.terminal().GetEvent())
	}
	if resumed.GetSuccess().GetAttributesDelta().AsMap()["ok"] != true {
		t.Errorf("expected attributes_delta to mirror upstream JSON, got %+v",
			resumed.GetSuccess().GetAttributesDelta().AsMap())
	}
}

// TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback pins the
// S-executors-http-node-error-class-field contract:
//
//	(1) A template author can configure WHICH JSON field in an upstream error
//	    body carries the upstream's own error-class token (here `code` instead of
//	    the default `error_class`), via the per-node `error_class_field`
//	    attribute. When the upstream returns an unexpected 4xx whose body names
//	    the class in the configured field, the terminal Error.error_class is
//	    `http/request_invalid/<that-token>` — read from the CONFIGURED field, not
//	    a hardcoded `error_class` key.
//	(2) When an unexpected 4xx body parses but carries NO error-class field at
//	    all, the http-node emits a stable, subscribable `/_unspecified` leaf
//	    (`http/request_invalid/_unspecified`) rather than collapsing to the
//	    catch-all `http/expectation_mismatch` — so subscribers/policies can
//	    pattern-match `http/request_invalid/*` even for taxonomy-less upstreams.
//
// Both cases drive the real executeCore against real httptest upstreams. The
// value-delivering component is the real http-node executor classifying real
// upstream 4xx bodies. A 400 status is used (outside the default expect_status
// 2xx set and distinct from the 429-park / 5xx-server_error branches) so the
// dispatch resolves through classifyUnexpectedStatus's 4xx arm.
func TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback(t *testing.T) {
	t.Run("ConfiguredFieldRead", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			// @constraint: body deliberately omits `error_class` and names the token only in `code`, so a hardcoded-`error_class` reader would find nothing and the configured field is the only source of the token.
			_, _ = w.Write([]byte(`{"code":"quota_exhausted","message":"over quota"}`))
		}))
		defer ts.Close()

		s := testServer(t, false)
		c := &collector{}
		req := newRequest(t, map[string]any{
			"url":               ts.URL,
			"method":            "GET",
			"error_class_field": "code",
		})
		_ = s.executeCore(context.Background(), req, c.send)

		errd := c.terminal().GetStreamClose().GetError()
		if errd == nil {
			t.Fatalf("expected Error terminal, got %+v", c.terminal().GetEvent())
		}
		if got, want := errd.GetErrorClass(), "http/request_invalid/quota_exhausted"; got != want {
			t.Errorf("error_class=%q, want %q (read from the configured `code` field)", got, want)
		}
	})

	t.Run("AbsentFieldUnspecifiedFallback", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			// @constraint: body parses but carries neither the default `error_class` nor any configured token field, forcing the fallback to the stable `/_unspecified` leaf instead of the catch-all.
			_, _ = w.Write([]byte(`{"message":"bad request","detail":"missing param"}`))
		}))
		defer ts.Close()

		s := testServer(t, false)
		c := &collector{}
		req := newRequest(t, map[string]any{"url": ts.URL, "method": "GET"})
		_ = s.executeCore(context.Background(), req, c.send)

		errd := c.terminal().GetStreamClose().GetError()
		if errd == nil {
			t.Fatalf("expected Error terminal, got %+v", c.terminal().GetEvent())
		}
		got := errd.GetErrorClass()
		if !strings.HasSuffix(got, "/_unspecified") {
			t.Errorf("error_class=%q, want a leaf ending in /_unspecified (e.g. http/request_invalid/_unspecified), NOT http/expectation_mismatch", got)
		}
		if got == "http/expectation_mismatch" {
			t.Errorf("error_class=%q: an absent error-class field must fall back to a stable /_unspecified leaf, not the catch-all expectation_mismatch", got)
		}
	})
}

// TestStubMode_RejectsNonObjectStubResponse covers the new spec §12.2
// constraint that attributes_delta is a JSON object — non-object
// stub_response values must be rejected as http/attribute_invalid.
func TestStubMode_RejectsNonObjectStubResponse(t *testing.T) {
	s := testServer(t, true)
	c := &collector{}
	req := newRequest(t, map[string]any{
		"url":           "http://unreachable.invalid/",
		"stub_response": "not-an-object",
	})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	errd := c.terminal().GetStreamClose().GetError()
	if errd == nil {
		t.Fatalf("expected Error, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http/attribute_invalid" {
		t.Errorf("error_class=%q, want http/attribute_invalid", errd.GetErrorClass())
	}
}
