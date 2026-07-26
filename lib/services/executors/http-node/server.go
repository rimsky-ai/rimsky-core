// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/execoutcome"
)

// @concept: executor
type Server struct {
	genv1.UnimplementedExecutorServer
	cfg      Opts
	client   *http.Client
	stubMode bool
	obs      *ObservabilityServer
}

func NewServer(cfg Opts) *Server {
	return &Server{
		cfg: cfg,
		// @decision: three-dispatch-deadlines
		client:   cfg.Egress.HTTPClient(0),
		stubMode: cfg.StubMode,
	}
}

func (s *Server) SetObservability(obs *ObservabilityServer) { s.obs = obs }

func (s *Server) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	nodeRunID := req.GetDispatchId()
	stepID := "http-node:" + req.GetNodeType()
	if s.obs != nil && nodeRunID != "" {
		s.obs.RegisterDispatch(nodeRunID)
		s.obs.AppendEvent(nodeRunID, MakeEvent(
			"step-"+stepID, "", "step_started",
			"http-node dispatch started",
			genv1.Severity_INFO,
			map[string]any{"step_id": stepID, "node_type": req.GetNodeType()},
		))
	}

	outcome, err := s.executeCore(ctx, req)
	s.recordTerminal(nodeRunID, stepID, outcome)
	return outcome, err
}

func (s *Server) recordTerminal(nodeRunID, stepID string, outcome *genv1.Outcome) {
	if s.obs == nil || nodeRunID == "" || outcome == nil {
		return
	}
	switch oc := outcome.GetOutcome().(type) {
	case *genv1.Outcome_Success:
		s.obs.AppendEvent(nodeRunID, MakeEvent(
			"step-complete-"+stepID, "step-"+stepID, "step_completed",
			"http-node dispatch completed",
			genv1.Severity_INFO,
			map[string]any{"step_id": stepID},
		))
		s.obs.MarkTerminal(nodeRunID)
	case *genv1.Outcome_Error:
		ec := oc.Error.GetErrorClass()
		s.obs.AppendEvent(nodeRunID, MakeEvent(
			"step-failed-"+stepID, "step-"+stepID, "step_failed",
			"http-node dispatch failed",
			genv1.Severity_ERROR,
			map[string]any{"step_id": stepID, "error": ec},
		))
		s.obs.AppendEvent(nodeRunID, MakeEvent(
			"error-"+stepID, "step-"+stepID, "error",
			ec,
			genv1.Severity_ERROR,
			map[string]any{"error": ec},
		))
		s.obs.MarkTerminal(nodeRunID)
	case *genv1.Outcome_Park:
		s.obs.AppendEvent(nodeRunID, MakeEvent(
			"step-parked-"+stepID, "step-"+stepID, "step_parked",
			"http-node dispatch parked",
			genv1.Severity_INFO,
			map[string]any{"step_id": stepID},
		))
		s.obs.MarkTerminal(nodeRunID)
	}
}

func (s *Server) executeCore(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	ud := req.GetAttributes().AsMap()

	if stubmode.IsProbe(ud) && s.stubMode {
		return s.executeStub(req), nil
	}
	if stubmode.IsParkProbe(ud) && s.stubMode {
		return s.executeParkProbe(ud, req.GetScratch()), nil
	}
	if stubmode.IsCancelProbe(ud) && s.stubMode {
		return nil, s.executeCancelProbe(ctx, req.GetCallbackUrl())
	}

	urlStr, _ := ud["url"].(string)
	if urlStr == "" {
		return execoutcome.Errored("http/attribute_invalid", "attributes.url required"), nil
	}

	if s.stubMode {
		return s.executeStub(req), nil
	}

	method, _ := ud["method"].(string)
	if method == "" {
		method = "GET"
	}

	expectStatus := defaultExpectStatus()
	if es, ok := ud["expect_status"].([]any); ok {
		parsed := make([]int, 0, len(es))
		for _, v := range es {
			f, ok := v.(float64)
			if !ok {
				return execoutcome.Errored("http/attribute_invalid",
					fmt.Sprintf("expect_status entry %v is not numeric", v)), nil
			}
			parsed = append(parsed, int(f))
		}
		expectStatus = parsed
	}

	reqBody, ctype, err := buildRequestBody(ud)
	if err != nil {
		return execoutcome.Errored("http/attribute_invalid", err.Error()), nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return execoutcome.Errored("http/attribute_invalid", err.Error()), nil
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
		return execoutcome.Errored(classifyTransportErr(err), err.Error()), nil
	}
	defer resp.Body.Close()

	limit := int64(s.cfg.MaxBodyBytes)
	if limit <= 0 {
		limit = 10 * 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return execoutcome.Errored(classifyTransportErr(err), "read body: "+err.Error()), nil
	}
	if int64(len(body)) > limit {
		return execoutcome.Errored("http/response_truncated",
			fmt.Sprintf("response body exceeds the %d-byte cap; refusing to deliver a truncated (possibly corrupt) body", limit)), nil
	}

	if resp.StatusCode == http.StatusTooManyRequests && !statusOK(resp.StatusCode, expectStatus) {
		resumeAt := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now)
		return parkedOutcome(resumeAt), nil
	}

	if !statusOK(resp.StatusCode, expectStatus) {
		errorClassField := s.cfg.ErrorClassField
		if ecf, ok := ud["error_class_field"].(string); ok && ecf != "" {
			errorClassField = ecf
		}
		if errorClassField == "" {
			errorClassField = DefaultErrorClassField
		}
		return execoutcome.Errored(
			classifyUnexpectedStatus(resp.StatusCode, body, errorClassField),
			fmt.Sprintf("status=%d, body=%s", resp.StatusCode, truncate(string(body), 512))), nil
	}

	delta, err := buildAttributesDelta(body, resp.Header.Get("Content-Type"))
	if err != nil {
		return execoutcome.Errored("http/response_unparseable", err.Error()), nil
	}

	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: delta,
		Changed:         true,
		ChangeSummary:   fmt.Sprintf("HTTP %d from %s", resp.StatusCode, urlStr),
	}}}, nil
}

var configAttributeKeys = map[string]struct{}{
	"url":                          {},
	"method":                       {},
	"headers":                      {},
	"body":                         {},
	"expect_status":                {},
	stubmode.ProbeAttribute:        {},
	"stub_response":                {},
	stubmode.TagsAttribute:         {},
	"error_class_field":            {},
	stubmode.ParkProbeAttribute:    {},
	stubmode.CancelProbeAttribute:  {},
	stubmode.ParkResumeAtAttribute: {},
}

func buildRequestBody(ud map[string]any) (io.Reader, string, error) {
	if b, ok := ud["body"]; ok && b != nil {
		switch bb := b.(type) {
		case string:
			return strings.NewReader(bb), "", nil
		default:
			jb, err := json.Marshal(bb)
			if err != nil {
				return nil, "", fmt.Errorf("body not JSON-serialisable: %w", err)
			}
			return bytes.NewReader(jb), "application/json", nil
		}
	}
	inputs := map[string]any{}
	for k, v := range ud {
		if _, isConfig := configAttributeKeys[k]; isConfig {
			continue
		}
		inputs[k] = v
	}
	if len(inputs) == 0 {
		return nil, "", nil
	}
	jb, err := json.Marshal(inputs)
	if err != nil {
		return nil, "", fmt.Errorf("attributes not JSON-serialisable: %w", err)
	}
	return bytes.NewReader(jb), "application/json", nil
}

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

func (s *Server) executeStub(req *genv1.ExecuteRequest) *genv1.Outcome {
	ud := req.GetAttributes().AsMap()
	delta := stubmode.ResponseDelta()
	if sr, ok := ud["stub_response"]; ok {
		m, ok := sr.(map[string]any)
		if !ok {
			return execoutcome.Errored("http/attribute_invalid", fmt.Sprintf("stub_response must be a JSON object, got %T", sr))
		}
		delta = m
	}
	v, err := structpb.NewStruct(delta)
	if err != nil {
		return execoutcome.Errored("http/attribute_invalid", "stub_response not JSON-representable: "+err.Error())
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		AttributesDelta: v,
		Changed:         true,
		ChangeSummary:   "stub",
		Scratch:         req.GetScratch(),
		Tags:            stubTags(ud),
	}}}
}

func stubTags(ud map[string]any) []string {
	raw, ok := ud[stubmode.TagsAttribute].([]any)
	if !ok {
		return nil
	}
	tags := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
}

var cancelProbeHTTPClient = &http.Client{Timeout: 5 * time.Second}

func postCancelProbeSignal(callbackURL, ackID string) {
	if callbackURL == "" {
		return
	}
	req, err := http.NewRequest(http.MethodPost, callbackURL+"/v1/callback/"+ackID,
		strings.NewReader(`{"success":{"changed":false}}`))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cancelProbeHTTPClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func (s *Server) executeCancelProbe(ctx context.Context, callbackURL string) error {
	postCancelProbeSignal(callbackURL, "cancel-observed")
	<-ctx.Done()
	postCancelProbeSignal(callbackURL, "cancel-acknowledged")
	return ctx.Err()
}

const defaultParkProbeScratch = "http-node-stub-park-scratch"

func (s *Server) executeParkProbe(ud map[string]any, incomingScratch []byte) *genv1.Outcome {
	park := &genv1.Park{
		ResumeAt: timestamppb.New(time.Now().Add(30 * time.Second)),
		Scratch:  incomingScratch,
	}
	if len(park.Scratch) == 0 {
		park.Scratch = []byte(defaultParkProbeScratch)
	}
	if raw, _ := ud[stubmode.ParkResumeAtAttribute].(string); raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			park.ResumeAt = timestamppb.New(t)
		}
	}
	return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: park}}
}

func classifyTransportErr(err error) string {
	if err == nil {
		return "http/network_error"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "http/timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "http/timeout"
	}
	return "http/network_error"
}

func classifyUnexpectedStatus(status int, body []byte, errorClassField string) string {
	if status >= 500 && status <= 599 {
		return fmt.Sprintf("http/server_error/%d", status)
	}
	if errorClassField == "" {
		errorClassField = DefaultErrorClassField
	}
	if status >= 400 && status <= 499 && len(body) > 0 {
		var decoded map[string]any
		if json.Unmarshal(body, &decoded) == nil {
			if cls, ok := decoded[errorClassField].(string); ok && cls != "" {
				return "http/request_invalid/" + cls
			}
			return "http/request_invalid/_unspecified"
		}
	}
	return "http/expectation_mismatch"
}

const defaultRetryAfter = 30 * time.Second

func parseRetryAfter(header string, now func() time.Time) time.Time {
	header = strings.TrimSpace(header)
	base := now()
	if header == "" {
		return base.Add(defaultRetryAfter)
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return base.Add(defaultRetryAfter)
		}
		return base.Add(time.Duration(secs) * time.Second)
	}
	for _, layout := range []string{http.TimeFormat, time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if t, err := time.Parse(layout, header); err == nil {
			if t.After(base) {
				return t
			}
			return base
		}
	}
	return base.Add(defaultRetryAfter)
}

func parkedOutcome(resumeAt time.Time) *genv1.Outcome {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Park{Park: &genv1.Park{
		ResumeAt: timestamppb.New(resumeAt),
		Tags:     []string{TagRateLimited},
	}}}
}

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
