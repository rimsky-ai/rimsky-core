// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package verifierhttp

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

// @concept: executor
type Server struct {
	genv1.UnimplementedExecutorServer
	stubMode bool
	client   *http.Client
}

func NewServer(stubMode bool) *Server {
	return &Server{
		stubMode: stubMode,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

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
			payloadMap["upstream_class"] = upstreamClass
		}
		payload, _ := structpb.NewStruct(payloadMap)
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: errClass,
			Payload:    payload,
		}}}, nil
	}

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

const defaultClassField = "class"

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

func statusInSet(n int, set []int) bool {
	for _, e := range set {
		if e == n {
			return true
		}
	}
	return false
}

func toFloatSet(in []int) []any {
	out := make([]any, len(in))
	for i, n := range in {
		out[i] = float64(n)
	}
	return out
}

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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

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

func erroredOutcome(class, msg string) *genv1.Outcome {
	payload, _ := structpb.NewStruct(map[string]any{"message": msg})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
		ErrorClass: class, Payload: payload,
	}}}
}

func stubSuccess() *genv1.Outcome {
	delta, _ := structpb.NewStruct(map[string]any{"stub": true})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: delta,
		Changed:         false,
		ChangeSummary:   "verifier-http stub",
	}}}
}
