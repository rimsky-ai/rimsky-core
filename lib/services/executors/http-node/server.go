// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// sendFunc is the narrow sender interface used by executeCore so the same
// logic can drive both the gRPC stream transport and the HTTP+JSON bridge.
type sendFunc func(*genv1.ExecuteEvent) error

// Server implements genv1.ExecutorServer. It owns the http.Client used
// for upstream requests and the stub-mode flag.
type Server struct {
	genv1.UnimplementedExecutorServer
	cfg      Config
	client   *http.Client
	stubMode bool
	// obs, when non-nil, receives per-dispatch trace events. Set by
	// main.go after the gRPC server is constructed (the observability
	// surface is registered on the same listener).
	obs *ObservabilityServer
}

// NewServer builds a Server with a timeout-configured http.Client.
func NewServer(cfg Config) *Server {
	return &Server{
		cfg:      cfg,
		client:   &http.Client{Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond},
		stubMode: cfg.StubMode,
	}
}

// SetObservability attaches an ObservabilityServer so executeCore can
// emit per-dispatch trace events. Optional: when nil, dispatch runs
// without trace emission.
func (s *Server) SetObservability(obs *ObservabilityServer) { s.obs = obs }

// Execute is the gRPC-facing entrypoint. Adapts the streaming server to the
// sendFunc-based core logic.
func (s *Server) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	return s.executeCore(stream.Context(), req, stream.Send)
}

// executeCore is the transport-independent execution body.
//
// @agent-contract executeCore: runs the http-node cell's network
// request and emits one terminal StreamClose ExecuteEvent via send,
// with a Success or Error outcome on the wire. Called by the gRPC
// Execute method and by the HTTP+JSON bridge. Handles stub_mode
// (short-circuits before network), JSON and non-JSON responses,
// custom expect_status lists, and user-supplied headers.
// Post-userdata-collapse, the executor reads its full input from the
// unified `attributes` bag. A fixed set of attribute keys (`url`,
// `method`, `headers`, `body`, `expect_status`, `stub_probe`,
// `stub_response`, `error_class_field` — see `configAttributeKeys`)
// drives the transport (`error_class_field` names the JSON field read
// from an upstream 4xx error body to derive the
// `http/request_invalid/<token>` leaf, defaulting to `error_class`);
// every other attribute key is serialised as the implicit JSON request
// body via `buildRequestBody`'s configAttributeKeys subtraction. An
// explicit `attributes.body` overrides the implicit body. Rimsky
// validates the substituted attribute bag against the executor's
// expected attribute schema; the executor sees the resolved values
// verbatim. Does NOT paginate, stream response bodies, or honor
// redirects beyond Go stdlib defaults. Does NOT itself retry; instead
// an unexpected 429 resolves to a StreamClose Park
// (PARK_REASON_SNOOZE) with a resume_at computed from Retry-After, so
// the supervisor's SweepParkedNodes auto-wakes and re-dispatches the
// node — reusing rimsky's existing parked-node auto-wake mechanism,
// not new park machinery. Reentrant; the http.Client is safe for
// concurrent use.
func (s *Server) executeCore(ctx context.Context, req *genv1.ExecuteRequest, send sendFunc) error {
	// @deliberate: emit an opening heartbeat unconditionally so observers see liveness.
	_ = send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Heartbeat{Heartbeat: &genv1.Heartbeat{
		TimestampMs: time.Now().UnixMilli(),
		Note:        "http-node starting",
	}}})

	// @deliberate: emit a step_started trace event for the dashboard's tree view.
	dispatchID := req.GetDispatchId()
	stepID := "http-node:" + req.GetNodeType()
	if s.obs != nil && dispatchID != "" {
		// @constraint: register the dispatch with the in-memory ledger so subsequent
		// AppendEvent / MarkTerminal calls succeed; forged dispatch IDs (issue 13)
		// cannot create ledger records this way.
		s.obs.RegisterDispatch(dispatchID)
		s.obs.AppendEvent(dispatchID, MakeEvent(
			"step-"+stepID, "", "step_started",
			"http-node dispatch started",
			genv1.Severity_INFO,
			map[string]any{"step_id": stepID, "node_type": req.GetNodeType()},
		))
	}
	// @deliberate: wrap send so executeCore's StreamClose events also update the
	// trace + mark the dispatch terminal in the same code path.
	origSend := send
	send = func(ev *genv1.ExecuteEvent) error {
		if s.obs != nil && dispatchID != "" {
			if sc := ev.GetStreamClose(); sc != nil {
				switch oc := sc.Outcome.(type) {
				case *genv1.StreamClose_Success:
					_ = oc
					s.obs.AppendEvent(dispatchID, MakeEvent(
						"step-complete-"+stepID, "step-"+stepID, "step_completed",
						"http-node dispatch completed",
						genv1.Severity_INFO,
						map[string]any{"step_id": stepID},
					))
					s.obs.MarkTerminal(dispatchID)
				case *genv1.StreamClose_Error:
					ec := oc.Error.GetErrorClass()
					s.obs.AppendEvent(dispatchID, MakeEvent(
						"step-failed-"+stepID, "step-"+stepID, "step_failed",
						"http-node dispatch failed",
						genv1.Severity_ERROR,
						map[string]any{"step_id": stepID, "error": ec},
					))
					s.obs.AppendEvent(dispatchID, MakeEvent(
						"error-"+stepID, "step-"+stepID, "error",
						ec,
						genv1.Severity_ERROR,
						map[string]any{"error": ec},
					))
					s.obs.MarkTerminal(dispatchID)
				}
			}
		}
		return origSend(ev)
	}

	ud := req.GetAttributes().AsMap()

	// @constraint: conformance-probe escape hatch — the conformance harness uses
	// executor-agnostic attributes flagged `stub_probe: true`. When stub mode is
	// on, short-circuit before per-executor shape validation so the suite's
	// basic-happy-path scenarios work regardless of which executor is under test.
	// Scenarios that intentionally exercise malformed-shape rejection (e.g.
	// `malformed_attributes`) omit the flag.
	if probe, _ := ud["stub_probe"].(bool); probe && s.stubMode {
		return s.executeStub(req, send)
	}

	// @constraint: validate attribute shape even in stub mode — the protocol
	// contract requires executors to reject malformed input consistently, not
	// only in live mode. Spec §14.4 + conformance `malformed_attributes`
	// scenario.
	urlStr, _ := ud["url"].(string)
	if urlStr == "" {
		return sendErrored(send, "http/attribute_invalid", "attributes.url required")
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

	// @constraint: body composition per spec §5.8 — http-node puts the per-run
	// `attributes` in the request body. `attributes.body` (if present) is an
	// explicit override useful for fixtures and ad-hoc payloads — when set, it
	// wins. Otherwise the JSON-serialised `attributes` map becomes the body.
	// Empty attributes + no override → no body.
	reqBody, ctype, err := buildRequestBody(ud, req.GetAttributes().AsMap())
	if err != nil {
		return sendErrored(send, "http/attribute_invalid", err.Error())
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return sendErrored(send, "http/attribute_invalid", err.Error())
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
		return sendErrored(send, classifyTransportErr(err), err.Error())
	}
	defer resp.Body.Close()

	limit := int64(s.cfg.MaxBodyBytes)
	if limit <= 0 {
		limit = 10 * 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return sendErrored(send, classifyTransportErr(err), "read body: "+err.Error())
	}

	// @constraint: 429 Too Many Requests is a transient rate-limit signal, not a
	// hard terminal — park (SNOOZE) with a resume_at computed from Retry-After
	// so the supervisor's existing SweepParkedNodes wakes the node and
	// re-dispatches, rather than terminating the run. Reuses rimsky's
	// Park-outcome + resume_at auto-wake mechanism (the same PARK_REASON_SNOOZE
	// + ResumeAt shape claude-agent's rate-limit path emits) — no new park
	// machinery. This precedes the generic !statusOK error branch so a 429 is
	// never collapsed into an http/expectation_mismatch hard Error.
	//
	// @deliberate: a 429 that the template's expect_status explicitly accepts is
	// treated as a normal success per the operator's declared contract — only an
	// unexpected 429 parks.
	if resp.StatusCode == http.StatusTooManyRequests && !statusOK(resp.StatusCode, expectStatus) {
		resumeAt := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now)
		return sendParked(send, resumeAt,
			fmt.Sprintf("upstream returned 429; parked until %s (Retry-After=%q)", resumeAt.Format(time.RFC3339), resp.Header.Get("Retry-After")))
	}

	if !statusOK(resp.StatusCode, expectStatus) {
		// @constraint: resolve the upstream error-class field name in priority
		// order — a per-node `attributes.error_class_field` wins, else the
		// executor's configured default (`error_class` unless overridden by env).
		errorClassField := s.cfg.ErrorClassField
		if ecf, ok := ud["error_class_field"].(string); ok && ecf != "" {
			errorClassField = ecf
		}
		if errorClassField == "" {
			errorClassField = DefaultErrorClassField
		}
		return sendErrored(send, classifyUnexpectedStatus(resp.StatusCode, body, errorClassField), fmt.Sprintf("status=%d, body=%s", resp.StatusCode, truncate(string(body), 512)))
	}

	// @constraint: response → attributes_delta — the target's response body must
	// be a JSON object so it can map directly to the spec §12.2
	// StreamClose-Success `attributes_delta` Struct (which the supervisor merges
	// into rimsky_node_attributes.data). Non-object JSON is rejected; non-JSON
	// content types are wrapped in a base64 envelope under known keys.
	delta, err := buildAttributesDelta(body, resp.Header.Get("Content-Type"))
	if err != nil {
		return sendErrored(send, "http/response_unparseable", err.Error())
	}

	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         true,
			ChangeSummary:   fmt.Sprintf("HTTP %d from %s", resp.StatusCode, urlStr),
		}}},
	}})
}

// configAttributeKeys is the set of attribute keys the http-node
// executor treats as transport configuration (URL, method, headers,
// body override, etc.). When building the implicit request body from
// the attribute bag, these keys are subtracted so transport config
// never leaks into the upstream payload.
var configAttributeKeys = map[string]struct{}{
	"url":               {},
	"method":            {},
	"headers":           {},
	"body":              {},
	"expect_status":     {},
	"stub_probe":        {},
	"stub_response":     {},
	"error_class_field": {},
}

// buildRequestBody picks the upstream request body. `attributes.body`
// is an explicit override (string passed verbatim, structured value
// JSON-marshalled with implicit application/json). When absent, the
// per-run input attributes (`attrs` minus known config keys) are
// JSON-marshalled. When the resulting input bag is empty, no body is
// sent.
//
// Under the 2026-05-21 userdata collapse `ud` and `attrs` are typically
// the same map (the unified attribute bag); subtracting config keys
// from the implicit-body path keeps the two roles distinguishable.
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
	inputs := map[string]any{}
	for k, v := range attrs {
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
	return strings.NewReader(string(jb)), "application/json", nil
}

// buildAttributesDelta turns the upstream response into a Struct suitable for
// StreamClose-Success.attributes_delta. JSON object responses are passed
// through as-is. Non-JSON responses are wrapped as
// `{body_base64, content_type}` so the caller still sees the bytes. JSON
// arrays / scalars are an error: the attributes shape is by spec a JSON
// object.
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
// Returns attributes.stub_response if provided, else {stub: true}. The response
// becomes the StreamClose-Success.attributes_delta — must be a JSON object
// per spec §12.2.
func (s *Server) executeStub(req *genv1.ExecuteRequest, send sendFunc) error {
	ud := req.GetAttributes().AsMap()
	delta := map[string]any{"stub": true}
	if sr, ok := ud["stub_response"]; ok {
		m, ok := sr.(map[string]any)
		if !ok {
			return sendErrored(send, "http/attribute_invalid", fmt.Sprintf("stub_response must be a JSON object, got %T", sr))
		}
		delta = m
	}
	v, err := structpb.NewStruct(delta)
	if err != nil {
		return sendErrored(send, "http/attribute_invalid", "stub_response not JSON-representable: "+err.Error())
	}
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: v,
			Changed:         true,
			ChangeSummary:   "stub",
		}}},
	}})
}

// classifyTransportErr maps a transport-layer error to a hierarchical
// error class per `concept:signal`. Distinguishes deadline-exceeded /
// network-timeout errors (which operators typically want to retry with
// backoff) from generic network errors.
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

// classifyUnexpectedStatus maps an unexpected HTTP status to a
// hierarchical error class per `concept:signal`:
//   - 5xx → `http/server_error/<status>` (upstream may recover; subscribers
//     can pattern-match `http/server_error/*` for transient flavors).
//   - 4xx with a parseable JSON object body whose configured error-class
//     field (`errorClassField`, default `error_class`, overridable per-node
//     via `attributes.error_class_field` or by env) holds a non-empty token →
//     `http/request_invalid/<body_class>` (the upstream named a specific
//     request defect).
//   - 4xx with a parseable JSON object body that does NOT carry the configured
//     field → `http/request_invalid/_unspecified` (a stable, subscribable leaf
//     so `http/request_invalid/*` policies still match taxonomy-less upstreams,
//     rather than collapsing to the catch-all `http/expectation_mismatch`).
//   - otherwise (non-4xx/5xx, empty body, or unparseable body) →
//     `http/expectation_mismatch`.
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
			// @constraint: 4xx body parsed as a JSON object but the configured
			// error-class field is absent/empty — emit the stable `_unspecified`
			// leaf so the request-invalid subtree stays subscribable even for
			// upstreams that publish no taxonomy.
			return "http/request_invalid/_unspecified"
		}
	}
	return "http/expectation_mismatch"
}

// defaultRetryAfter is the snooze window used when a 429 carries no
// Retry-After header (or an unparseable one). RFC 9110 makes Retry-After
// optional on 429, so we still need a finite resume_at so the supervisor's
// SweepParkedNodes auto-wakes the node rather than leaving it parked forever.
const defaultRetryAfter = 30 * time.Second

// parseRetryAfter computes the wall-clock resume time from a Retry-After
// header value per RFC 9110 §10.2.3, which permits two forms:
//   - delta-seconds: a non-negative integer count of seconds to wait
//     (e.g. "7") → now + 7s.
//   - HTTP-date: an absolute IMF-fixdate timestamp (e.g.
//     "Wed, 21 Oct 2026 07:28:00 GMT") → that instant.
//
// `now` is injected (rather than calling time.Now directly) so the
// delta-seconds branch is deterministically testable. An empty or
// malformed header (including a negative delta) falls back to
// now + defaultRetryAfter; a parseable HTTP-date that is not in the
// future returns now itself (the upstream is explicitly clearing the
// wait). Either way the result is a finite resume_at, which the
// supervisor's auto-wake sweep requires to fire.
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
			// @deliberate: a non-future date is treated as "retry now" rather
			// than the generic default — the upstream is explicitly clearing
			// the wait.
			return base
		}
	}
	return base.Add(defaultRetryAfter)
}

// sendParked emits a terminal StreamClose Park with PARK_REASON_SNOOZE and the
// computed resume_at, reusing rimsky's existing parked-node auto-wake
// mechanism. The supervisor's SweepParkedNodes wakes the node at resume_at and
// re-dispatches it (resume_reason = "deadline_elapsed").
func sendParked(send sendFunc, resumeAt time.Time, note string) error {
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Park{Park: &genv1.Park{
			Reason:     genv1.ParkReason_PARK_REASON_SNOOZE,
			ResumeAt:   timestamppb.New(resumeAt),
			ReasonNote: note,
		}}},
	}})
}

func sendErrored(send sendFunc, class, msg string) error {
	payload, _ := structpb.NewStruct(map[string]any{"error": msg})
	return send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
			ErrorClass: class,
			Payload:    payload,
		}}},
	}})
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
