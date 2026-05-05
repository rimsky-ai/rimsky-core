// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
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

func newRequestWithAttrs(t *testing.T, ud, attrs map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	udStruct, err := structpb.NewStruct(ud)
	if err != nil {
		t.Fatalf("structpb userdata: %v", err)
	}
	req := &genv1.ExecuteRequest{NodeType: "http.request@1", Userdata: udStruct}
	if attrs != nil {
		attrStruct, err := structpb.NewStruct(attrs)
		if err != nil {
			t.Fatalf("structpb attributes: %v", err)
		}
		req.Attributes = attrStruct
	}
	return req
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
	cmp := term.GetComplete()
	if cmp == nil {
		t.Fatalf("expected Complete, got %T", term.GetEvent())
	}
	if !cmp.GetChanged() {
		t.Error("expected changed=true")
	}
	got := cmp.GetAttributesDelta().AsMap()
	if got["ok"] != true || got["name"] != "alice" {
		t.Errorf("unexpected attributes_delta: %+v", got)
	}
	// First event should be a heartbeat.
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

	errd := c.terminal().GetErrored()
	if errd == nil {
		t.Fatalf("expected Errored terminal, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http_unexpected_status" {
		t.Errorf("error_class=%q, want http_unexpected_status", errd.GetErrorClass())
	}
}

func TestExecute_NetworkError_ReturnsHTTPRequestFailed(t *testing.T) {
	s := testServer(t, false)
	c := &collector{}
	// Use a port that nothing is listening on.
	req := newRequest(t, map[string]any{"url": "http://127.0.0.1:1/does-not-exist"})
	_ = s.executeCore(context.Background(), req, c.send)

	errd := c.terminal().GetErrored()
	if errd == nil {
		t.Fatalf("expected Errored terminal, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http_request_failed" {
		t.Errorf("error_class=%q, want http_request_failed", errd.GetErrorClass())
	}
}

func TestExecute_MalformedUserdata_MissingURL(t *testing.T) {
	s := testServer(t, false)
	c := &collector{}
	req := newRequest(t, map[string]any{"method": "GET"})
	_ = s.executeCore(context.Background(), req, c.send)

	errd := c.terminal().GetErrored()
	if errd == nil {
		t.Fatalf("expected Errored terminal, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "invalid_userdata" {
		t.Errorf("error_class=%q, want invalid_userdata", errd.GetErrorClass())
	}
}

func TestStubMode_ReturnsCannedResponse(t *testing.T) {
	s := testServer(t, true)
	c := &collector{}
	// No upstream server will be contacted; URL is ignored in stub mode.
	req := newRequest(t, map[string]any{"url": "http://unreachable.invalid/"})
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}

	cmp := c.terminal().GetComplete()
	if cmp == nil {
		t.Fatalf("expected Complete, got %+v", c.terminal().GetEvent())
	}
	got := cmp.GetAttributesDelta().AsMap()
	if got["stub"] != true {
		t.Errorf("expected stub:true, got %+v", got)
	}
	if cmp.GetChangeSummary() != "stub" {
		t.Errorf("change_summary=%q, want stub", cmp.GetChangeSummary())
	}
}

// TestStubMode_RejectsMalformedUserdata verifies the protocol contract that
// executors validate userdata shape consistently in both stub and live modes
// (Spec §14.4 + conformance `malformed_userdata` scenario).
func TestStubMode_RejectsMalformedUserdata(t *testing.T) {
	s := testServer(t, true)
	c := &collector{}
	req := newRequest(t, map[string]any{}) // no url
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	if len(c.events) < 2 {
		t.Fatalf("expected >=2 events (heartbeat + errored), got %d", len(c.events))
	}
	errd := c.terminal().GetErrored()
	if errd == nil {
		t.Fatalf("expected Errored terminal, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "invalid_userdata" {
		t.Errorf("error_class=%q, want invalid_userdata", errd.GetErrorClass())
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
	got := c.terminal().GetComplete().GetAttributesDelta().AsMap()
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
	cmp := c.terminal().GetComplete()
	if cmp == nil {
		t.Fatalf("expected Complete, got %+v", c.terminal().GetEvent())
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
	reqProto := &genv1.ExecuteRequest{NodeType: "http.request@1", Userdata: ud}
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
	if last.GetComplete() == nil {
		t.Fatalf("expected Complete terminal, got %+v", last.GetEvent())
	}
	got := last.GetComplete().GetAttributesDelta().AsMap()
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
	got := c.terminal().GetComplete().GetAttributesDelta().AsMap()
	if _, ok := got["body_base64"]; !ok {
		t.Errorf("expected body_base64, got %+v", got)
	}
	if got["content_type"] != "text/plain" {
		t.Errorf("content_type=%v", got["content_type"])
	}
}

// TestExecute_JSONContentType_InvalidBody_ReturnsParseFailed covers the
// http_response_parse_failed error class.
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
	errd := c.terminal().GetErrored()
	if errd == nil {
		t.Fatalf("expected Errored, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http_response_parse_failed" {
		t.Errorf("error_class=%q, want http_response_parse_failed", errd.GetErrorClass())
	}
}

// TestExecute_PostWithStructBody verifies that a userdata.body override is
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
		// Attributes are present but should be ignored when userdata.body
		// overrides.
		map[string]any{"name": "ignored", "extra": "ignored"},
	)
	if err := s.executeCore(context.Background(), req, c.send); err != nil {
		t.Fatalf("executeCore: %v", err)
	}
	if got["name"] != "bob" {
		t.Errorf("upstream got: %+v", got)
	}
	if _, ok := got["extra"]; ok {
		t.Errorf("attributes leaked when userdata.body overrides: %+v", got)
	}
}

// TestExecute_AttributesAsRequestBody verifies the spec §5.8 contract that
// http-node POSTs the per-run `attributes` map as the JSON request body when
// no explicit userdata.body override is set.
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
	delta := c.terminal().GetComplete().GetAttributesDelta().AsMap()
	if delta["upstream_ack"] != true {
		t.Errorf("expected attributes_delta to mirror upstream JSON body, got %+v", delta)
	}
}

// TestExecute_NoAttributesNoBody verifies that with neither userdata.body nor
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
	errd := c.terminal().GetErrored()
	if errd == nil {
		t.Fatalf("expected Errored, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "http_response_parse_failed" {
		t.Errorf("error_class=%q, want http_response_parse_failed", errd.GetErrorClass())
	}
}

// TestStubMode_RejectsNonObjectStubResponse covers the new spec §12.2
// constraint that attributes_delta is a JSON object — non-object
// stub_response values must be rejected as invalid_userdata.
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
	errd := c.terminal().GetErrored()
	if errd == nil {
		t.Fatalf("expected Errored, got %+v", c.terminal().GetEvent())
	}
	if errd.GetErrorClass() != "invalid_userdata" {
		t.Errorf("error_class=%q, want invalid_userdata", errd.GetErrorClass())
	}
}
