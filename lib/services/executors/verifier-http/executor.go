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
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/checkspec"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/execoutcome"
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
		client:   &http.Client{},
	}
}

func (s *Server) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	ud := req.GetAttributes().AsMap()
	if probe, _ := ud["stub_probe"].(bool); probe && s.stubMode {
		return execoutcome.StubSuccess("verifier-http stub"), nil
	}
	urlStr, _ := ud["url"].(string)
	if urlStr == "" {
		return execoutcome.Errored("verifier/attribute_invalid", "attributes.url required"), nil
	}
	if s.stubMode {
		return execoutcome.StubSuccess("verifier-http stub"), nil
	}
	timeout := 60 * time.Second
	if ms, ok := checkspec.Numeric(ud["timeout_ms"]); ok && ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	expected := defaultExpectedStatus()
	if es, ok := ud["expected_status"].([]any); ok && len(es) > 0 {
		var exact []int
		for _, v := range es {
			if n, ok := checkspec.Numeric(v); ok {
				exact = append(exact, int(n))
			}
		}
		expected = expectedStatusSet{exact: exact, isExplicit: true}
	}
	var bodyReader io.Reader
	if body, ok := ud["body"]; ok && body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return execoutcome.Errored("verifier/attribute_invalid", "body not JSON-serialisable: "+err.Error()), nil
		}
		bodyReader = bytes.NewReader(raw)
	}

	dispatchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(dispatchCtx, http.MethodPost, urlStr, bodyReader)
	if err != nil {
		return execoutcome.Errored("verifier/attribute_invalid", err.Error()), nil
	}
	if bodyReader != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return execoutcome.Errored(classifyTransportErr(err), err.Error()), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if !expected.matches(resp.StatusCode) {
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
			"expected_status": expected.describe(),
			"body_preview":    truncate(string(respBody), 512),
		}
		if upstreamClass != "" {
			payloadMap["upstream_class"] = upstreamClass
		}
		payload, err := structpb.NewStruct(payloadMap)
		if err != nil {
			delete(payloadMap, "body_preview")
			payload, err = structpb.NewStruct(payloadMap)
			if err != nil {
				payload, _ = structpb.NewStruct(map[string]any{"actual_status": float64(resp.StatusCode)})
			}
		}
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

type expectedStatusSet struct {
	exact      []int
	isExplicit bool
}

func defaultExpectedStatus() expectedStatusSet {
	return expectedStatusSet{}
}

func (s expectedStatusSet) matches(code int) bool {
	if !s.isExplicit {
		return code >= 200 && code < 300
	}
	for _, e := range s.exact {
		if e == code {
			return true
		}
	}
	return false
}

func (s expectedStatusSet) describe() any {
	if !s.isExplicit {
		return "2xx"
	}
	out := make([]any, len(s.exact))
	for i, n := range s.exact {
		out[i] = float64(n)
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return strings.ToValidUTF8(s, "")
	}
	return strings.ToValidUTF8(s[:max-1], "") + "…"
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
