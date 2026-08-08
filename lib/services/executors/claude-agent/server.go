// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/internal/observability"
)

type PostCallbackFn func(url string, body map[string]any, logger *slog.Logger)

type ServerConfig struct {
	Opts          Opts
	CliRunner     CliRunner
	Observability *ObservabilityServer
	Logger        *slog.Logger
	PostCallback  PostCallbackFn
}

func (c *ServerConfig) fill() {
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.PostCallback == nil {
		c.PostCallback = DefaultPostCallback
	}
	if c.CliRunner == nil {
		c.CliRunner = NewClaudeCliRunner(CliRunnerOpts{
			Auth:       c.Opts.Auth,
			BinaryPath: c.Opts.ClaudeBinary,
		})
	}
}

type dispatchInputs struct {
	NodeID                       string
	InstanceID                   string
	NodeType                     string
	NodeRunID                    string
	RunScopeID                   string
	PriorNodeRunID               string
	PriorDispatchDispositionWire string
	CallbackURL                  string
	CancelToken                  string
	Attributes                   map[string]any
	AttributesSchema             map[string]any
	ClaimProducers               map[string]any
	SessionToken                 string
}

type ExecutorServer struct {
	genv1.UnimplementedExecutorServer
	cfg ServerConfig
}

func NewExecutorServer(cfg ServerConfig) *ExecutorServer {
	cfg.fill()
	return &ExecutorServer{cfg: cfg}
}

func (s *ExecutorServer) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	if stubmode.IsCancelProbe(req.GetAttributes().AsMap()) && StubModeEnabled() {
		return nil, executeCancelProbe(ctx, req.GetCallbackUrl())
	}
	ackID := uuid.NewString()
	runID := req.GetDispatchId()
	if runID == "" {
		runID = uuid.NewString()
	}
	traceID := req.GetDispatchId()
	if traceID == "" {
		traceID = ackID
	}
	logger := s.cfg.Logger.With(
		"run_id", runID,
		"node_id", req.GetNodeId(),
		"node_type", req.GetNodeType(),
		"dispatch_id", req.GetDispatchId(),
	)

	attributes := req.GetAttributes().AsMap()
	logger.Info("execute.received",
		"instance_id", req.GetInstanceId(),
		"model", stringOrEmpty(attributes["model"]),
		"cwd_from_claim_producer", stringOrEmpty(attributes["cwd_from_claim_producer"]),
		"claim_producers", claimProducerNames(req.GetClaimProducers()))

	inputs := dispatchInputs{
		NodeID:                       nodeIDOr(req.GetNodeId(), runID),
		InstanceID:                   req.GetInstanceId(),
		NodeType:                     nodeTypeOr(req.GetNodeType()),
		NodeRunID:                    req.GetDispatchId(),
		RunScopeID:                   req.GetRunScopeId(),
		PriorNodeRunID:               req.GetPriorDispatchId(),
		PriorDispatchDispositionWire: priorDispositionWire(req),
		CallbackURL:                  req.GetCallbackUrl(),
		CancelToken:                  req.GetCancelToken(),
		Attributes:                   attributes,
		AttributesSchema:             req.GetAttributesSchema().AsMap(),
		ClaimProducers:               unwrapClaimProducersProto(req.GetClaimProducers()),
		SessionToken:                 sessionTokenOr(SessionTokenFromScratch(req.GetScratch()), attributes),
	}

	s.recordDispatchStart(traceID, inputs)

	go s.runAndCallback(inputs, traceID, runID, logger, func(base string) string {
		return buildCallbackURL(base, ackID)
	}, nil)

	return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
		AsyncAckId:           ackID,
		ExpectedCompletionMs: 0,
	}}}, nil
}

func (s *ExecutorServer) recordDispatchStart(traceID string, inputs dispatchInputs) {
	if s.cfg.Observability == nil {
		return
	}
	s.cfg.Observability.RegisterDispatch(traceID)
	s.cfg.Observability.AppendEvent(traceID, makeTraceEvent("step_started", genv1.Severity_INFO, "", map[string]any{
		"step_id":   "dispatch",
		"node_id":   inputs.NodeID,
		"node_type": inputs.NodeType,
	}))
}

func makeTraceEvent(category string, sev genv1.Severity, message string, attrs map[string]any) *genv1.TraceEvent {
	return observability.MakeEvent(uuid.NewString(), "", category, message, sev, attrs)
}

func (s *ExecutorServer) runAndCallback(
	inputs dispatchInputs,
	traceID string,
	runID string,
	logger *slog.Logger,
	callbackURLFor func(base string) string,
	decorateBody func(body map[string]any),
) {
	defer func() {
		if r := recover(); r != nil {
			s.handleDispatchPanic(inputs, traceID, r, logger, callbackURLFor, decorateBody)
		}
	}()
	cliConfig, err := ParseCliConfig(inputs.Attributes["cli"])
	if err != nil {
		s.postFailure(inputs, traceID, err, logger, callbackURLFor, decorateBody)
		return
	}

	outcome := RunAgent(AgentRunOptions{
		SessionID:                    runID,
		NodeID:                       inputs.NodeID,
		NodeType:                     inputs.NodeType,
		InstanceID:                   inputs.InstanceID,
		Model:                        stringOr(inputs.Attributes["model"], "claude-sonnet-4-5"),
		SystemPrompt:                 stringOr(inputs.Attributes["system_prompt"], ""),
		UserPrompt:                   stringOr(inputs.Attributes["user_prompt"], ""),
		AttributesSchema:             inputs.AttributesSchema,
		Attributes:                   inputs.Attributes,
		ClaimProducers:               inputs.ClaimProducers,
		CwdFromClaimProducer:         stringOrEmpty(inputs.Attributes["cwd_from_claim_producer"]),
		CwdOverride:                  stringOrEmpty(inputs.Attributes["cwd"]),
		CliConfig:                    cliConfig,
		McpAllowlist:                 s.cfg.Opts.McpAllowlist,
		ExposeEnvAllowlist:           s.cfg.Opts.ExposeEnvAllowlist,
		NodeRunID:                    inputs.NodeRunID,
		RunScopeID:                   inputs.RunScopeID,
		PriorNodeRunID:               inputs.PriorNodeRunID,
		PriorDispatchDispositionWire: inputs.PriorDispatchDispositionWire,
		CallbackURL:                  inputs.CallbackURL,
		CancelToken:                  inputs.CancelToken,
		CliRunner:                    s.cfg.CliRunner,
		SilenceTimeoutMsDefault:      s.cfg.Opts.SilenceTimeoutMsDefault,
		ToolUseTimeoutMsDefault:      s.cfg.Opts.ToolUseTimeoutMsDefault,
		Logger:                       logger,
		SessionToken:                 inputs.SessionToken,
	})

	body := OutcomeToCallbackBody(outcome)
	if decorateBody != nil {
		decorateBody(body)
	}
	if s.cfg.Observability != nil {
		attrs := map[string]any{"step_id": "dispatch"}
		var cat string
		switch outcome.Kind {
		case OutcomeComplete:
			cat = "step_completed"
		case OutcomeErrored:
			cat = "step_failed"
			attrs["error"] = outcome.ErrorClass
		case OutcomeBlocked:
			cat = "step_blocked"
			attrs["reason"] = outcome.Reason
		case OutcomeParkRequested:
			cat = "step_parked"
			attrs["reason"] = outcome.Reason
		}
		s.cfg.Observability.AppendEvent(traceID, makeTraceEvent(cat, genv1.Severity_INFO, "", attrs))
		s.cfg.Observability.MarkTerminal(traceID)
	}
	if inputs.CallbackURL != "" {
		s.cfg.PostCallback(callbackURLFor(inputs.CallbackURL), body, logger)
	} else {
		logger.Warn("no callback_url; outcome dropped", "outcome", outcome.Kind)
	}
}

func (s *ExecutorServer) postFailure(
	inputs dispatchInputs,
	traceID string,
	err error,
	logger *slog.Logger,
	callbackURLFor func(base string) string,
	decorateBody func(body map[string]any),
) {
	errorClass := "agent/internal_error"
	var cliConfigErr *CliConfigError
	if errors.As(err, &cliConfigErr) {
		errorClass = cliConfigErr.ErrorClass()
	}
	logger.Error("agent run failed", "error", err.Error(), "error_class", errorClass)
	if s.cfg.Observability != nil {
		s.cfg.Observability.AppendEvent(traceID, makeTraceEvent("error", genv1.Severity_ERROR, "", map[string]any{
			"error":       err.Error(),
			"error_class": errorClass,
		}))
		s.cfg.Observability.MarkTerminal(traceID)
	}
	if inputs.CallbackURL == "" {
		return
	}
	body := map[string]any{
		"error": map[string]any{
			"error_class": errorClass,
			"payload":     map[string]any{"error": err.Error()},
		},
	}
	if decorateBody != nil {
		decorateBody(body)
	}
	s.cfg.PostCallback(callbackURLFor(inputs.CallbackURL), body, logger)
}

func (s *ExecutorServer) handleDispatchPanic(
	inputs dispatchInputs,
	traceID string,
	recovered any,
	logger *slog.Logger,
	callbackURLFor func(base string) string,
	decorateBody func(body map[string]any),
) {
	const errorClass = "agent/internal_error"
	msg := fmt.Sprintf("%v", recovered)
	logger.Error("agent run panicked", "panic", msg, "error_class", errorClass, "stack", string(debug.Stack()))
	if s.cfg.Observability != nil {
		s.cfg.Observability.AppendEvent(traceID, makeTraceEvent("error", genv1.Severity_ERROR, "", map[string]any{
			"error":       msg,
			"error_class": errorClass,
		}))
		s.cfg.Observability.MarkTerminal(traceID)
	}
	if inputs.CallbackURL == "" {
		return
	}
	body := map[string]any{
		"error": map[string]any{
			"error_class": errorClass,
			"payload":     map[string]any{"error": msg},
		},
	}
	if decorateBody != nil {
		decorateBody(body)
	}
	s.cfg.PostCallback(callbackURLFor(inputs.CallbackURL), body, logger)
}

var cancelProbeHTTPClient = &http.Client{Timeout: 5 * time.Second}

func postCancelProbeSignal(callbackURL, ackID string) {
	if callbackURL == "" {
		return
	}
	req, err := http.NewRequest(http.MethodPost, buildCallbackURL(callbackURL, ackID),
		bytes.NewReader([]byte(`{"success":{"changed":false}}`)))
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

func executeCancelProbe(ctx context.Context, callbackURL string) error {
	postCancelProbeSignal(callbackURL, stubmode.CancelObservedAck)
	<-ctx.Done()
	postCancelProbeSignal(callbackURL, stubmode.CancelAcknowledgedAck)
	return ctx.Err()
}

func buildCallbackURL(base string, ackID string) string {
	trimmed := strings.TrimRight(base, "/")
	return trimmed + "/v1/callback/" + url.PathEscape(ackID)
}

func nodeIDOr(nodeID string, fallback string) string {
	if nodeID == "" {
		return fallback
	}
	return nodeID
}

func nodeTypeOr(nodeType string) string {
	if nodeType == "" {
		return "unknown"
	}
	return nodeType
}

// @decision: claude-agent-session-attribute
func sessionTokenOr(fromScratch string, attributes map[string]any) string {
	if fromScratch != "" {
		return fromScratch
	}
	return stringOrEmpty(attributes["session_token"])
}

func priorDispositionWire(req *genv1.ExecuteRequest) string {
	if req.PriorDispatchDisposition == nil {
		return ""
	}
	return req.GetPriorDispatchDisposition().String()
}

func claimProducerNames(claimProducers map[string]*genv1.ClaimProducerHandle) []string {
	names := make([]string, 0, len(claimProducers))
	for name := range claimProducers {
		names = append(names, name)
	}
	return names
}

var defaultCallbackHTTPClient = &http.Client{Timeout: callbackPostTimeout}

func DefaultPostCallback(callbackURL string, body map[string]any, logger *slog.Logger) {
	PostCallbackVia(defaultCallbackHTTPClient)(callbackURL, body, logger)
}

const (
	callbackPostMaxAttempts = 5
	callbackPostBaseDelay   = 200 * time.Millisecond
	callbackPostTimeout     = 10 * time.Second
)

func PostCallbackVia(client *http.Client) PostCallbackFn {
	return func(callbackURL string, body map[string]any, logger *slog.Logger) {
		raw, err := json.Marshal(body)
		if err != nil {
			logger.Error("callback POST body marshal failed", "error", err.Error(), "url", callbackURL)
			return
		}
		delay := callbackPostBaseDelay
		for attempt := 1; attempt <= callbackPostMaxAttempts; attempt++ {
			if postCallbackOnce(client, callbackURL, raw, attempt, logger) {
				return
			}
			if attempt == callbackPostMaxAttempts {
				logger.Error("callback POST exhausted retries; outcome dropped", "attempts", attempt, "url", callbackURL)
				return
			}
			time.Sleep(delay)
			delay *= 2
		}
	}
}

func postCallbackOnce(client *http.Client, callbackURL string, raw []byte, attempt int, logger *slog.Logger) bool {
	resp, err := client.Post(callbackURL, "application/json", bytes.NewReader(raw))
	if err != nil {
		logger.Warn("callback POST failed", "attempt", attempt, "error", err.Error(), "url", callbackURL)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		logger.Warn("callback POST returned non-2xx", "attempt", attempt, "status", resp.StatusCode, "url", callbackURL)
		return false
	}
	return true
}

type RunningGrpcServer struct {
	Address string
	server  *grpc.Server
}

func (r *RunningGrpcServer) Shutdown(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		r.server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		r.server.Stop()
		<-done
	}
}

func StartGrpcServer(host string, port int, executor *ExecutorServer, observability *ObservabilityServer, identity *peerauth.Identity) (*RunningGrpcServer, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	srv := grpc.NewServer(identity.GRPCServerOptions()...)
	genv1.RegisterExecutorServer(srv, executor)
	if observability != nil {
		genv1.RegisterExecutorObservabilityServer(srv, observability)
	}
	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil {
			executor.cfg.Logger.Error("grpc serve", "error", serveErr.Error())
		}
	}()
	return &RunningGrpcServer{
		Address: listener.Addr().String(),
		server:  srv,
	}, nil
}
