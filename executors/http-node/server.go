package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// sendFunc is the narrow sender interface used by executeCore so the same
// logic can drive both the gRPC stream transport and the HTTP+JSON bridge.
type sendFunc func(*genv1.ExecuteEvent) error

// Server implements genv1.NodeExecutorServer. It owns the http.Client used
// for upstream requests and the stub-mode flag.
type Server struct {
	genv1.UnimplementedNodeExecutorServer
	cfg      Config
	client   *http.Client
	stubMode bool
}

// NewServer builds a Server with a timeout-configured http.Client.
func NewServer(cfg Config) *Server {
	return &Server{
		cfg:      cfg,
		client:   &http.Client{Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond},
		stubMode: cfg.StubMode,
	}
}

// Execute is the gRPC-facing entrypoint. Adapts the streaming server to the
// sendFunc-based core logic.
func (s *Server) Execute(req *genv1.ExecuteRequest, stream genv1.NodeExecutor_ExecuteServer) error {
	return s.executeCore(stream.Context(), req, stream.Send)
}

// executeCore is the transport-independent execution body.
//
//	@agent-contract: executeCore
//	what: runs the http-node cell's network request and emits one terminal
//	      ExecuteEvent via send (Complete | Errored).
//	how:  called by the gRPC Execute method and by the HTTP+JSON bridge.
//	handles: stub_mode (short-circuits before network), JSON and non-JSON
//	         responses, custom expect_status lists, user-supplied headers,
//	         per-run attributes posted as the request body. Userdata is
//	         opaque executor configuration (url, method, headers,
//	         expect_status, optional body override); rimsky never inspects
//	         it.
//	does not: retry, paginate, stream response bodies, or honor redirects
//	          beyond Go stdlib defaults.
//	thread-safety: reentrant; the http.Client is safe for concurrent use.
func (s *Server) executeCore(ctx context.Context, req *genv1.ExecuteRequest, send sendFunc) error {
	// Always emit an opening heartbeat so observers see liveness.
	_ = send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Heartbeat{Heartbeat: &genv1.Heartbeat{
		TimestampMs: time.Now().UnixMilli(),
		Note:        "http-node starting",
	}}})

	// Validate userdata shape even in stub mode — the protocol contract
	// requires executors to reject malformed input consistently, not only
	// in live mode. Spec §14.4 + conformance `malformed_userdata` scenario.
	ud := req.GetUserdata().AsMap()
	urlStr, _ := ud["url"].(string)
	if urlStr == "" {
		return sendErrored(send, "invalid_userdata", "userdata.url required")
	}

	if s.stubMode {
		return s.executeStub(req, send)
	}

	method, _ := ud["method"].(string)
	if method == "" {
		method = "GET"
	}

	expectStatus := defaultExpectStatus()
	if es, ok := ud["expect_status"].([]any); ok {
		expectStatus = expectStatus[:0]
		for _, v := range es {
			if f, ok := v.(float64); ok {
				expectStatus = append(expectStatus, int(f))
			}
		}
	}

	// Body composition: per spec §5.8, http-node puts the per-run
	// `attributes` in the request body. `userdata.body` (if present) is an
	// explicit override useful for fixtures and ad-hoc payloads — when set,
	// it wins. Otherwise the JSON-serialised `attributes` map becomes the
	// body. Empty attributes + no override → no body.
	reqBody, ctype, err := buildRequestBody(ud, req.GetAttributes().AsMap())
	if err != nil {
		return sendErrored(send, "invalid_userdata", err.Error())
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return sendErrored(send, "invalid_userdata", err.Error())
	}
	if hdrs, ok := ud["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			if sv, ok := v.(string); ok {
				httpReq.Header.Set(k, sv)
			}
		}
	}
	if httpReq.Header.Get("Accept") == "" {
		httpReq.Header.Set("Accept", "application/json")
	}
	if ctype != "" && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", ctype)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return sendErrored(send, "http_request_failed", err.Error())
	}
	defer resp.Body.Close()

	limit := int64(s.cfg.MaxBodyBytes)
	if limit <= 0 {
		limit = 10 * 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return sendErrored(send, "http_request_failed", "read body: "+err.Error())
	}

	if !statusOK(resp.StatusCode, expectStatus) {
		return sendErrored(send, "http_unexpected_status", fmt.Sprintf("status=%d, body=%s", resp.StatusCode, truncate(string(body), 512)))
	}

	// Response → attributes_delta. The target's response body must be a
	// JSON object so it can map directly to the spec §12.2 Complete
	// `attributes_delta` Struct (which the supervisor merges into
	// rimsky_node_attributes.data). Non-object JSON is rejected; non-JSON
	// content types are wrapped in a base64 envelope under known keys.
	delta, err := buildAttributesDelta(body, resp.Header.Get("Content-Type"))
	if err != nil {
		return sendErrored(send, "http_response_parse_failed", err.Error())
	}

	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{Complete: &genv1.Complete{
		AttributesDelta: delta,
		Changed:         true,
		ChangeSummary:   fmt.Sprintf("HTTP %d from %s", resp.StatusCode, urlStr),
	}}})
}

// buildRequestBody picks the upstream request body. `userdata.body` is an
// explicit override (string passed verbatim, structured value JSON-marshalled
// with implicit application/json). When absent, the per-run `attributes` map
// is JSON-marshalled. When attributes is also empty, no body is sent.
func buildRequestBody(ud, attrs map[string]any) (io.Reader, string, error) {
	if b, ok := ud["body"]; ok && b != nil {
		switch bb := b.(type) {
		case string:
			return strings.NewReader(bb), "", nil
		default:
			jb, err := json.Marshal(bb)
			if err != nil {
				return nil, "", fmt.Errorf("body not JSON-serialisable: %w", err)
			}
			return strings.NewReader(string(jb)), "application/json", nil
		}
	}
	if len(attrs) == 0 {
		return nil, "", nil
	}
	jb, err := json.Marshal(attrs)
	if err != nil {
		return nil, "", fmt.Errorf("attributes not JSON-serialisable: %w", err)
	}
	return strings.NewReader(string(jb)), "application/json", nil
}

// buildAttributesDelta turns the upstream response into a Struct suitable for
// Complete.attributes_delta. JSON object responses are passed through as-is.
// Non-JSON responses are wrapped as `{body_base64, content_type}` so the
// caller still sees the bytes. JSON arrays / scalars are an error: the
// attributes shape is by spec a JSON object.
func buildAttributesDelta(body []byte, contentType string) (*structpb.Struct, error) {
	if !strings.Contains(contentType, "json") {
		return structpb.NewStruct(map[string]any{
			"body_base64":  base64.StdEncoding.EncodeToString(body),
			"content_type": contentType,
		})
	}
	if len(body) == 0 {
		return structpb.NewStruct(map[string]any{})
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("response JSON is not an object (got %T); attributes_delta requires an object", decoded)
	}
	return structpb.NewStruct(m)
}

// executeStub short-circuits the network path; used when RIMSKY_EXECUTOR_STUB_MODE=1.
// Returns userdata.stub_response if provided, else {stub: true}. The response
// becomes the Complete.attributes_delta — must be a JSON object per spec §12.2.
func (s *Server) executeStub(req *genv1.ExecuteRequest, send sendFunc) error {
	ud := req.GetUserdata().AsMap()
	delta := map[string]any{"stub": true}
	if sr, ok := ud["stub_response"]; ok {
		m, ok := sr.(map[string]any)
		if !ok {
			return sendErrored(send, "invalid_userdata", fmt.Sprintf("stub_response must be a JSON object, got %T", sr))
		}
		delta = m
	}
	v, err := structpb.NewStruct(delta)
	if err != nil {
		return sendErrored(send, "invalid_userdata", "stub_response not JSON-representable: "+err.Error())
	}
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{Complete: &genv1.Complete{
		AttributesDelta: v,
		Changed:         true,
		ChangeSummary:   "stub",
	}}})
}

func sendErrored(send sendFunc, class, msg string) error {
	payload, _ := structpb.NewStruct(map[string]any{"error": msg})
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Errored{Errored: &genv1.Errored{
		ErrorClass: class,
		Payload:    payload,
	}}})
}

// defaultExpectStatus returns the default accepted status list (2xx).
func defaultExpectStatus() []int {
	return []int{200, 201, 202, 203, 204, 205, 206, 207, 208, 226}
}

func statusOK(code int, expect []int) bool {
	for _, s := range expect {
		if code == s {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
