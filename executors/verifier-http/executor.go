// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — verifier-http bundled verifier executor. POSTs a
// caller-supplied payload to a configured URL; verifies the response
// status matches the operator's expected set.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Verifier executors / verifier-http.
//
//	@concept: verifier-pattern
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

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
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
		payload, _ := structpb.NewStruct(map[string]any{
			"actual_status":   float64(resp.StatusCode),
			"expected_status": toFloatSet(expected),
			"body_preview":    truncate(string(respBody), 512),
		})
		return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
				ErrorClass: "verifier/check_failed",
				Payload:    payload,
			}}},
		}})
	}

	// Successful verify: no state change, but surface a small
	// attributes delta with the response status for downstream reads.
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
//	@source: executors/http-node/server.go::classifyTransportErr
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
