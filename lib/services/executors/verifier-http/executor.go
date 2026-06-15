// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package main — verifier-http bundled verifier executor. POSTs a
// caller-supplied payload to a configured URL; verifies the response
// status matches the operator's expected set.
//
// @deliberate: implements the verifier-executor pattern (a regular
// executor that co-holds the upstream claim, runs checks, and returns
// success or error) — documentation-only pattern, no successor concept
// per the concepts catalog.
//
// Attribute schema:
//
//	{
//	  "url": "https://verifier.example.com/check",
//	  "body": {"...": "..."},                  // sent as the POST body
//	  "expected_status": [200, 204],            // default [200]
//	  "timeout_ms": 30000
//	}
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// Server implements genv1.ExecutorServer.
type Server struct {
	genv1.UnimplementedExecutorServer
	stubMode bool
	client   *http.Client
}

// NewServer constructs a Server with a per-instance http client.
func NewServer(stubMode bool) *Server {
	return &Server{
		stubMode: stubMode,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

// sendFunc is the narrow sender shape shared with the test fake.
type sendFunc func(*genv1.ExecuteEvent) error

// Execute is the gRPC entrypoint.
func (s *Server) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	return s.executeCore(stream.Context(), req, stream.Send)
}

// executeCore is transport-neutral so tests can drive it directly.
func (s *Server) executeCore(ctx context.Context, req *genv1.ExecuteRequest, send sendFunc) error {
	ud := req.GetAttributes().AsMap()
	if probe, _ := ud["stub_probe"].(bool); probe && s.stubMode {
		return send(stubSuccess())
	}
	urlStr, _ := ud["url"].(string)
	if urlStr == "" {
		return sendErrored(send, "verifier/attribute_invalid", "attributes.url required")
	}
	if s.stubMode {
		return send(stubSuccess())
	}
	timeout := 60 * time.Second
	if ms, ok := numeric(ud["timeout_ms"]); ok && ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	expected := []int{http.StatusOK}
	if es, ok := ud["expected_status"].([]any); ok && len(es) > 0 {
		expected = expected[:0]
		for _, v := range es {
			if n, ok := numeric(v); ok {
				expected = append(expected, int(n))
			}
		}
	}
	var bodyReader io.Reader
	if body, ok := ud["body"]; ok && body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return sendErrored(send, "verifier/attribute_invalid", "body not JSON-serialisable: "+err.Error())
		}
		bodyReader = bytes.NewReader(raw)
	}

	dispatchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(dispatchCtx, http.MethodPost, urlStr, bodyReader)
	if err != nil {
		return sendErrored(send, "verifier/attribute_invalid", err.Error())
	}
	if bodyReader != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return sendErrored(send, classifyTransportErr(err), err.Error())
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if !statusInSet(resp.StatusCode, expected) {
		// @constraint: surface the upstream's typed class on the error class so
		// concept:signal policy/subscriber matching keys on the upstream's
		// taxonomy rather than collapsing to the generic verifier/check_failed
		// leaf. The class is read from a configurable JSON field on the upstream
		// body (default `class`, per attributes.class_field). Mirrors http-node's
		// configured-error-class-field discipline so the spec's "error with the
		// upstream's class" Acceptance is satisfied structurally — relying on a
		// body_preview text scrape would let "class field dropped" pass silently.
		classField := defaultClassField
		if cf, ok := ud["class_field"].(string); ok && cf != "" {
			classField = cf
		}
		upstreamClass := extractClassField(respBody, classField)
		errClass := "verifier/check_failed"
		if upstreamClass != "" {
			errClass = "verifier/check_failed/" + upstreamClass
		}
		payloadMap := map[string]any{
			"actual_status":   float64(resp.StatusCode),
			"expected_status": toFloatSet(expected),
			"body_preview":    truncate(string(respBody), 512),
		}
		if upstreamClass != "" {
			// @deliberate: echo the typed class on the payload too so downstream
			// readers can inspect it without parsing body_preview.
			payloadMap["upstream_class"] = upstreamClass
		}
		payload, _ := structpb.NewStruct(payloadMap)
		return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
				ErrorClass: errClass,
				Payload:    payload,
			}}},
		}})
	}

	// @deliberate: successful verify reports Changed:false (no state change) but
	// still emits a small attributes delta carrying the response status so
	// downstream reads can inspect it.
	delta, _ := structpb.NewStruct(map[string]any{
		"verifier_pass":   true,
		"verifier_status": float64(resp.StatusCode),
	})
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         false,
			ChangeSummary:   fmt.Sprintf("verifier-http: %d from %s", resp.StatusCode, urlStr),
		}}},
	}})
}

// defaultClassField is the JSON field the executor reads from a 4xx/5xx
// upstream body to derive the `verifier/check_failed/<class>` leaf when
// the per-node `attributes.class_field` is not set. Mirrors the
// http-node executor's `DefaultErrorClassField` discipline.
const defaultClassField = "class"

// extractClassField parses `body` as a JSON object and returns the
// string value at `field`. Returns "" if the body is empty, not a JSON
// object, or the field is missing/non-string. The "" sentinel routes
// the caller to the unqualified `verifier/check_failed` leaf.
func extractClassField(body []byte, field string) string {
	if len(body) == 0 || field == "" {
		return ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	cls, _ := decoded[field].(string)
	return cls
}

// statusInSet reports whether `n` is in the configured expected set.
func statusInSet(n int, set []int) bool {
	for _, e := range set {
		if e == n {
			return true
		}
	}
	return false
}

// toFloatSet converts []int → []any of float64 for Struct serialization.
func toFloatSet(in []int) []any {
	out := make([]any, len(in))
	for i, n := range in {
		out[i] = float64(n)
	}
	return out
}

// numeric is a small JSON-friendly numeric coercion helper.
func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// truncate clips a string to len bytes with a trailing ellipsis.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// classifyTransportErr maps a transport-layer error to a hierarchical
// error class per `concept:signal`. Distinguishes deadline-exceeded /
// network-timeout errors (which operators typically want to retry with
// backoff) from generic network errors.
//
//	@source: lib/services/executors/http-node/server.go::classifyTransportErr
func classifyTransportErr(err error) string {
	if err == nil {
		return "verifier/network_error"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "verifier/timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "verifier/timeout"
	}
	return "verifier/network_error"
}

func sendErrored(send sendFunc, class, msg string) error {
	payload, _ := structpb.NewStruct(map[string]any{"message": msg})
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
			ErrorClass: class, Payload: payload,
		}}},
	}})
}

func stubSuccess() *genv1.ExecuteEvent {
	delta, _ := structpb.NewStruct(map[string]any{"stub": true})
	return &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         false,
			ChangeSummary:   "verifier-http stub",
		}}},
	}}
}
