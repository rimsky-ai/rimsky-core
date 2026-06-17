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
//
// @concept: executor
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

// Execute is the gRPC unary entrypoint. Per TD-execute-rpc-unary the
// RPC returns exactly one settling Outcome — no stream, no per-event
// chunking.
func (s *Server) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	ud := req.GetAttributes().AsMap()
	if probe, _ := ud["stub_probe"].(bool); probe && s.stubMode {
		return stubSuccess(), nil
	}
	urlStr, _ := ud["url"].(string)
	if urlStr == "" {
		return erroredOutcome("verifier/attribute_invalid", "attributes.url required"), nil
	}
	if s.stubMode {
		return stubSuccess(), nil
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
			return erroredOutcome("verifier/attribute_invalid", "body not JSON-serialisable: "+err.Error()), nil
		}
		bodyReader = bytes.NewReader(raw)
	}

	dispatchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(dispatchCtx, http.MethodPost, urlStr, bodyReader)
	if err != nil {
		return erroredOutcome("verifier/attribute_invalid", err.Error()), nil
	}
	if bodyReader != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return erroredOutcome(classifyTransportErr(err), err.Error()), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if !statusInSet(resp.StatusCode, expected) {
		// @constraint: surface the upstream's typed class on the error
		// class so concept:signal policy/subscriber matching keys on
		// the upstream's taxonomy rather than collapsing to the generic
		// verifier/check_failed leaf.
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
			// @deliberate: echo the typed class on the payload too so
			// downstream readers can inspect it without parsing
			// body_preview.
			payloadMap["upstream_class"] = upstreamClass
		}
		payload, _ := structpb.NewStruct(payloadMap)
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: errClass,
			Payload:    payload,
		}}}, nil
	}

	// @deliberate: successful verify reports Changed:false (no state
	// change) but still emits a small attributes delta carrying the
	// response status so downstream reads can inspect it.
	delta, _ := structpb.NewStruct(map[string]any{
		"verifier_pass":   true,
		"verifier_status": float64(resp.StatusCode),
	})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: delta,
		Changed:         false,
		ChangeSummary:   fmt.Sprintf("verifier-http: %d from %s", resp.StatusCode, urlStr),
	}}}, nil
}

// defaultClassField is the JSON field the executor reads from a
// 4xx/5xx upstream body to derive the `verifier/check_failed/<class>`
// leaf when the per-node `attributes.class_field` is not set.
const defaultClassField = "class"

// extractClassField parses `body` as a JSON object and returns the
// string value at `field`. Returns "" when the body is empty, not a
// JSON object, or the field is missing/non-string.
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

// toFloatSet converts []int → []any of float64 for Struct
// serialization.
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
// error class per concept:signal.
//
//	@source: lib/services/executors/http-node/server.go::classifyTransportErr
//	@diverged: true
//	@reason: verifier-http uses the `verifier/` prefix in line with
//	concept:signal's executor-scoped error-class taxonomy; http-node
//	uses `http/`.
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

// erroredOutcome wraps a class + message into an Outcome{Error}.
func erroredOutcome(class, msg string) *genv1.Outcome {
	payload, _ := structpb.NewStruct(map[string]any{"message": msg})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: class, Payload: payload,
	}}}
}

// stubSuccess returns the canonical stub-mode Outcome{Success}
// carrying `{stub: true}` as the attributes_delta.
func stubSuccess() *genv1.Outcome {
	delta, _ := structpb.NewStruct(map[string]any{"stub": true})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: delta,
		Changed:         false,
		ChangeSummary:   "verifier-http stub",
	}}}
}
