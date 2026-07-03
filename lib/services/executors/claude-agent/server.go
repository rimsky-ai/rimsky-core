// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

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
	DispatchID                   string
	RunScopeID                   string
	PriorDispatchID              string
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

func (s *ExecutorServer) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
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
		"cwd_from_store", stringOrEmpty(attributes["cwd_from_store"]),
		"claim_producers", claimProducerNames(req.GetClaimProducers()))

	inputs := dispatchInputs{
		NodeID:                       nodeIDOr(req.GetNodeId(), runID),
		InstanceID:                   req.GetInstanceId(),
		NodeType:                     nodeTypeOr(req.GetNodeType()),
		DispatchID:                   req.GetDispatchId(),
		RunScopeID:                   req.GetRunScopeId(),
		PriorDispatchID:              req.GetPriorDispatchId(),
		PriorDispatchDispositionWire: priorDispositionWire(req),
		CallbackURL:                  req.GetCallbackUrl(),
		CancelToken:                  req.GetCancelToken(),
		Attributes:                   attributes,
		AttributesSchema:             req.GetAttributesSchema().AsMap(),
		ClaimProducers:               unwrapClaimProducersProto(req.GetClaimProducers()),
		SessionToken:                 sessionTokenOr(SessionTokenFromScratch(req.GetScratch()), attributes),
	}

	s.recordDispatchStart(traceID, inputs)

	go s.runAndCallback(inputs, ackID, traceID, runID, logger, func(base string) string {
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
	ackID string,
	traceID string,
	runID string,
	logger *slog.Logger,
	callbackURLFor func(base string) string,
	decorateBody func(body map[string]any),
) {
	time.Sleep(100 * time.Millisecond)

	cliConfig, err := ParseCliConfig(inputs.Attributes["cli"])
	if err != nil {
		s.postFailure(inputs, ackID, traceID, err, logger, callbackURLFor, decorateBody)
		return
	}

	outcome := RunAgent(AgentRunOptions{
		RunID:                        runID,
		NodeID:                       inputs.NodeID,
		NodeType:                     inputs.NodeType,
		InstanceID:                   inputs.InstanceID,
		Model:                        stringOr(inputs.Attributes["model"], "claude-sonnet-4-5"),
		SystemPrompt:                 stringOr(inputs.Attributes["system_prompt"], ""),
		UserPrompt:                   stringOr(inputs.Attributes["user_prompt"], ""),
		AttributesSchema:             inputs.AttributesSchema,
		Attributes:                   inputs.Attributes,
		ClaimProducers:               inputs.ClaimProducers,
		CwdFromStore:                 stringOrEmpty(inputs.Attributes["cwd_from_store"]),
		CwdOverride:                  stringOrEmpty(inputs.Attributes["cwd"]),
		CliConfig:                    cliConfig,
		McpAllowlist:                 s.cfg.Opts.McpAllowlist,
		ExposeEnvAllowlist:           s.cfg.Opts.ExposeEnvAllowlist,
		DispatchID:                   inputs.DispatchID,
		RunScopeID:                   inputs.RunScopeID,
		PriorDispatchID:              inputs.PriorDispatchID,
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
	ackID string,
	traceID string,
	err error,
	logger *slog.Logger,
	callbackURLFor func(base string) string,
	decorateBody func(body map[string]any),
) {
	errorClass := "agent/internal_error"
	if IsCliConfigError(err) {
		errorClass = "agent/attribute_invalid"
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
	_ = ackID
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

func DefaultPostCallback(callbackURL string, body map[string]any, logger *slog.Logger) {
	raw, err := json.Marshal(body)
	if err != nil {
		logger.Error("callback POST body marshal failed", "error", err.Error(), "url", callbackURL)
		return
	}
	resp, err := http.Post(callbackURL, "application/json", bytes.NewReader(raw))
	if err != nil {
		logger.Error("callback POST failed", "error", err.Error(), "url", callbackURL)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		logger.Warn("callback POST returned non-2xx", "status", resp.StatusCode, "url", callbackURL)
	}
}

type RunningGrpcServer struct {
	Address string
	server  *grpc.Server
}

func (r *RunningGrpcServer) Shutdown() {
	r.server.GracefulStop()
}

func StartGrpcServer(host string, port int, executor *ExecutorServer, observability *ObservabilityServer) (*RunningGrpcServer, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	srv := grpc.NewServer()
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
