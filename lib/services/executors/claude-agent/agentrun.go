// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

const (
	OutcomeComplete      = "complete"
	OutcomeBlocked       = "blocked"
	OutcomeErrored       = "errored"
	OutcomeParkRequested = "park_requested"
)

type AgentOutcome struct {
	Kind            string
	AttributesDelta map[string]any
	Changed         bool
	ChangeSummary   *string
	Reason          string
	Context         any
	ErrorClass      string
	Payload         any
	ReasonNote      string
	ResumeAt        *time.Time
	SessionToken    string
}

func erroredOutcome(errorClass string, payload any) AgentOutcome {
	return AgentOutcome{Kind: OutcomeErrored, ErrorClass: errorClass, Payload: payload}
}

type McpServerInput struct {
	Transport    string
	Name         string
	URL          string
	Headers      map[string]string
	Command      string
	Args         []string
	Env          map[string]string
	Module       string
	AllowedTools []string
}

type CliConfig struct {
	Bare                 bool
	PermissionMode       string
	AllowedTools         []string
	DisallowedTools      []string
	AddDirs              []string
	MaxBudgetUSD         string
	HandleRateLimits     *bool
	MaxSchemaCorrections *int
	McpServers           []McpServerInput
	ExposeEnv            []string
	RequiredSignoffs     []RequiredSignoff
	MaxSignoffAttempts   *int
	SilenceTimeoutMs     *int
	ToolUseTimeoutMs     *int
}

type AgentRunOptions struct {
	SessionID                    string
	NodeID                       string
	NodeType                     string
	InstanceID                   string
	Model                        string
	SystemPrompt                 string
	UserPrompt                   string
	AttributesSchema             map[string]any
	Attributes                   map[string]any
	ClaimProducers               map[string]any
	CwdFromStore                 string
	CwdOverride                  string
	CliConfig                    *CliConfig
	McpAllowlist                 Allowlist
	ExposeEnvAllowlist           Allowlist
	NodeRunID                    string
	RunScopeID                   string
	PriorNodeRunID               string
	PriorDispatchDispositionWire string
	CallbackURL                  string
	CancelToken                  string
	CliRunner                    CliRunner
	SilenceTimeoutMsDefault      int
	ToolUseTimeoutMsDefault      int
	Logger                       *slog.Logger
	SessionToken                 string
}

func RunAgent(opts AgentRunOptions) AgentOutcome {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	attrs := opts.Attributes
	if attrs == nil {
		attrs = map[string]any{}
		opts.Attributes = attrs
	}
	isProbe := (attrs["stub_probe"] == true || attrs["probe_park"] == true) && StubModeEnabled()
	if !isProbe {
		if reason := malformedAttributesReason(attrs); reason != "" {
			return erroredOutcome("agent/attribute_invalid", map[string]any{"reason": reason})
		}
	}
	if StubModeEnabled() {
		return runAgentStub(opts)
	}
	return runAgentReal(opts)
}

func malformedAttributesReason(attrs map[string]any) string {
	if _, ok := attrs["_invalid"]; ok {
		return "attributes._invalid present (reserved)"
	}
	if attrs["_missing_url"] == true {
		return "attributes._missing_url is set (reserved)"
	}
	return ""
}

func runAgentStub(opts AgentRunOptions) AgentOutcome {
	opts.Logger.Info("agent-run: stub mode", "run_id", opts.SessionID)
	time.Sleep(50 * time.Millisecond)
	attrs := opts.Attributes

	if attrs["probe_park"] == true {
		parkReason := "await_callback"
		if rawReason, present := attrs["park_reason"]; present {
			s, isString := rawReason.(string)
			if !isString || (s != "await_callback" && s != "snooze") {
				encoded, _ := json.Marshal(rawReason)
				return erroredOutcome("agent/attribute_invalid", map[string]any{
					"reason": fmt.Sprintf(`park_reason must be one of "await_callback" | "snooze", got %s`, encoded),
				})
			}
			parkReason = s
		}
		reasonNote := ""
		if note, ok := attrs["park_reason_note"].(string); ok {
			reasonNote = note
		}
		var resumeAt *time.Time
		if parkReason == "snooze" {
			t := time.Now().Add(30 * time.Second)
			resumeAt = &t
		}
		return AgentOutcome{
			Kind:       OutcomeParkRequested,
			Reason:     parkReason,
			ReasonNote: reasonNote,
			ResumeAt:   resumeAt,
		}
	}

	summary := "stub"
	if attrs["stub_probe"] != true {
		return AgentOutcome{
			Kind:            OutcomeComplete,
			AttributesDelta: map[string]any{"stub": true, "session_token": opts.SessionID},
			Changed:         true,
			ChangeSummary:   &summary,
		}
	}

	if stubResponse, present := attrs["stub_response"]; present {
		obj, isObject := stubResponse.(map[string]any)
		if !isObject {
			return erroredOutcome("agent/attribute_invalid", map[string]any{
				"reason": fmt.Sprintf("stub_response must be a JSON object, got %T", stubResponse),
			})
		}
		delta := map[string]any{}
		for k, v := range obj {
			delta[k] = v
		}
		delta["session_token"] = opts.SessionID
		return AgentOutcome{
			Kind:            OutcomeComplete,
			AttributesDelta: delta,
			Changed:         true,
			ChangeSummary:   &summary,
		}
	}
	return AgentOutcome{
		Kind:            OutcomeComplete,
		AttributesDelta: map[string]any{"stub": true, "session_token": opts.SessionID},
		Changed:         true,
		ChangeSummary:   &summary,
	}
}

type agentRunState struct {
	mu                       sync.Mutex
	handle                   CliHandle
	schemaCorrectionFailures int
	signoffFailures          int
	lastStdoutAt             time.Time
	openToolUses             map[string]openToolUse
	stderrBuf                string
	streamParser             *CliStreamParser

	resolved           atomic.Bool
	teardownInProgress atomic.Bool
	outcomeCh          chan AgentOutcome
	teardownDone       chan struct{}
	teardownDoneOnce   sync.Once
	silenceStopped     atomic.Bool
}

type openToolUse struct {
	name      string
	startedAt time.Time
}

func (s *agentRunState) safeResolve(outcome AgentOutcome) {
	if s.resolved.CompareAndSwap(false, true) {
		s.outcomeCh <- outcome
	}
}

func (s *agentRunState) closeTeardownDone() {
	s.teardownDoneOnce.Do(func() { close(s.teardownDone) })
}

func (s *agentRunState) currentHandle() CliHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handle
}

func runAgentReal(opts AgentRunOptions) AgentOutcome {
	logger := opts.Logger
	cliConfig := opts.CliConfig
	if cliConfig == nil {
		cliConfig = &CliConfig{}
	}
	silenceTimeoutMs := opts.SilenceTimeoutMsDefault
	if cliConfig.SilenceTimeoutMs != nil {
		silenceTimeoutMs = *cliConfig.SilenceTimeoutMs
	}
	toolUseTimeoutMs := opts.ToolUseTimeoutMsDefault
	if cliConfig.ToolUseTimeoutMs != nil {
		toolUseTimeoutMs = *cliConfig.ToolUseTimeoutMs
	}

	cwd, cwdErr := resolveCwd(opts.ClaimProducers, opts.CwdFromStore, opts.CwdOverride)
	if cwdErr != "" {
		return erroredOutcome("agent/attribute_invalid", map[string]any{"error": cwdErr})
	}

	callbackToken := uuid.NewString()
	renderedUser := opts.UserPrompt + "\n\n---\n" +
		"callback_token: " + callbackToken + "\n" +
		"binding_id: " + opts.NodeRunID + "\n" +
		"---\n"

	dispatchMcp, err := StartInternalMcpServer(InternalMcpOpts{Host: "127.0.0.1", Logger: logger})
	if err != nil {
		return erroredOutcome("agent/internal_error", map[string]any{"error": err.Error()})
	}
	var dispatchMcpCloseOnce sync.Once
	closeDispatchMcp := func() {
		dispatchMcpCloseOnce.Do(func() {
			if err := dispatchMcp.Close(); err != nil {
				logger.Warn("dispatch MCP close failed", "run_id", opts.SessionID, "error", err.Error())
			}
		})
	}

	var catalogTeardowns []func() error
	var catalogTeardownOnce sync.Once
	tearDownCatalogServers := func() {
		catalogTeardownOnce.Do(func() {
			for _, td := range catalogTeardowns {
				if err := td(); err != nil {
					logger.Warn("catalog server teardown failed", "run_id", opts.SessionID, "error", err.Error())
				}
			}
		})
	}

	var validateAttributes *jsonschema.Schema
	if len(opts.AttributesSchema) > 0 {
		compiled, err := compileAttributesSchema(opts.AttributesSchema)
		if err != nil {
			closeDispatchMcp()
			return erroredOutcome("agent/attribute_invalid", map[string]any{
				"errors": []string{"invalid attributes_schema: " + err.Error()},
			})
		}
		validateAttributes = compiled
	}

	state := &agentRunState{
		lastStdoutAt: time.Now(),
		openToolUses: map[string]openToolUse{},
		streamParser: NewCliStreamParser(),
		outcomeCh:    make(chan AgentOutcome, 1),
		teardownDone: make(chan struct{}),
	}

	teardownCli := func() {
		h := state.currentHandle()
		if h == nil {
			state.closeTeardownDone()
			return
		}
		state.teardownInProgress.Store(true)
		h.SendSigterm()
		waitExitWithTimeout(h, 5*time.Second)
		h.SendSigkill()
		if !waitExitWithTimeout(h, 5*time.Second) {
			logger.Warn("teardownCli: child did not exit within 5s of SIGKILL", "run_id", opts.SessionID)
		}
		state.closeTeardownDone()
	}

	maxSchemaCorrections := 3
	if cliConfig.MaxSchemaCorrections != nil && *cliConfig.MaxSchemaCorrections >= 0 {
		maxSchemaCorrections = *cliConfig.MaxSchemaCorrections
	}
	maxSignoffAttempts := 3
	if cliConfig.MaxSignoffAttempts != nil && *cliConfig.MaxSignoffAttempts >= 0 {
		maxSignoffAttempts = *cliConfig.MaxSignoffAttempts
	}

	rejectWithCorrection := func(detail string, scheduleTeardown ScheduleTeardown) CompleteResult {
		state.mu.Lock()
		state.schemaCorrectionFailures++
		failures := state.schemaCorrectionFailures
		state.mu.Unlock()
		if failures > maxSchemaCorrections {
			logger.Warn("report_complete: schema corrections exhausted; committing errored",
				"run_id", opts.SessionID, "failures", failures, "max", maxSchemaCorrections)
			scheduleTeardown(func() error {
				teardownCli()
				state.safeResolve(erroredOutcome("agent/schema_violation", map[string]any{
					"attempts":   failures,
					"max":        maxSchemaCorrections,
					"last_error": detail,
				}))
				return nil
			})
			return CompleteResult{Accepted: true}
		}
		return CompleteResult{Accepted: false, Errors: map[string][]string{
			"attributes_delta": {fmt.Sprintf("%s (correction %d/%d)", detail, failures, maxSchemaCorrections)},
		}}
	}

	rejectSignoff := func(detail string, scheduleTeardown ScheduleTeardown) CompleteResult {
		state.mu.Lock()
		state.signoffFailures++
		failures := state.signoffFailures
		state.mu.Unlock()
		if failures > maxSignoffAttempts {
			logger.Warn("report_complete: sign-offs unobtained; committing errored",
				"run_id", opts.SessionID, "failures", failures, "max", maxSignoffAttempts)
			scheduleTeardown(func() error {
				teardownCli()
				state.safeResolve(erroredOutcome("agent/signoff_unobtained", map[string]any{
					"attempts":   failures,
					"max":        maxSignoffAttempts,
					"last_error": detail,
				}))
				return nil
			})
			return CompleteResult{Accepted: true}
		}
		return CompleteResult{Accepted: false, Errors: map[string][]string{
			"signoffs": {fmt.Sprintf("%s (signoff %d/%d)", detail, failures, maxSignoffAttempts)},
		}}
	}

	dispatchMcp.Registry.Register(callbackToken, &TokenEntry{
		SessionID:         opts.SessionID,
		AttributesAtSpawn: opts.Attributes,
		CancelToken:       opts.CancelToken,
		NodeID:            opts.NodeID,
		CallbackURL:       opts.CallbackURL,
		DispatchContext: NewDispatchContextSnapshot(
			opts.NodeRunID,
			opts.RunScopeID,
			opts.PriorNodeRunID,
			opts.PriorDispatchDispositionWire,
			func(event WireContractViolation) {
				logger.Warn("dispatch_context: "+event.Message,
					"run_id", opts.SessionID,
					"kind", event.Kind,
					"prior_dispatch_id", event.PriorNodeRunID,
					"prior_dispatch_disposition_wire", event.PriorDispatchDispositionWire)
			},
		),
		OnComplete: func(attributesDelta map[string]any, changed bool, changeSummary *string, signoffs []string, scheduleTeardown ScheduleTeardown) (CompleteResult, error) {
			if attributesDelta != nil && validateAttributes != nil {
				merged := map[string]any{}
				for k, v := range opts.Attributes {
					merged[k] = v
				}
				for k, v := range attributesDelta {
					merged[k] = v
				}
				if err := validateAttributes.Validate(merged); err != nil {
					return rejectWithCorrection(schemaValidationDetail(err), scheduleTeardown), nil
				}
			}
			state.mu.Lock()
			state.schemaCorrectionFailures = 0
			state.mu.Unlock()

			effectiveBag := map[string]any{}
			for k, v := range attributesDelta {
				effectiveBag[k] = v
			}
			effectiveBag["session_token"] = opts.SessionID

			required := cliConfig.RequiredSignoffs
			if len(required) > 0 {
				if opts.NodeRunID == "" {
					logger.Warn("report_complete: sign-off gate configured but node_run_id empty; committing errored",
						"run_id", opts.SessionID)
					scheduleTeardown(func() error {
						teardownCli()
						state.safeResolve(erroredOutcome("agent/signoff_unobtained", map[string]any{
							"error": "node_run_id required for sign-off gate but was empty",
						}))
						return nil
					})
					return CompleteResult{Accepted: true}, nil
				}
				res := VerifyRequiredSignoffs(required, effectiveBag, opts.NodeRunID, signoffs)
				if !res.OK {
					detail := ""
					for i, u := range res.Unmet {
						if i > 0 {
							detail += ", "
						}
						detail += u.Path + ":" + u.Reason
					}
					return rejectSignoff("unmet sign-offs: "+detail, scheduleTeardown), nil
				}
				state.mu.Lock()
				state.signoffFailures = 0
				state.mu.Unlock()
			}

			scheduleTeardown(func() error {
				teardownCli()
				state.safeResolve(AgentOutcome{
					Kind:            OutcomeComplete,
					AttributesDelta: effectiveBag,
					Changed:         changed,
					ChangeSummary:   changeSummary,
				})
				return nil
			})
			return CompleteResult{Accepted: true}, nil
		},
		OnBlocked: func(reason string, context any, scheduleTeardown ScheduleTeardown) error {
			scheduleTeardown(func() error {
				teardownCli()
				state.safeResolve(AgentOutcome{Kind: OutcomeBlocked, Reason: reason, Context: context})
				return nil
			})
			return nil
		},
		OnError: func(errorClass string, payload any, scheduleTeardown ScheduleTeardown) error {
			scheduleTeardown(func() error {
				teardownCli()
				state.safeResolve(erroredOutcome(errorClass, payload))
				return nil
			})
			return nil
		},
		OnPark: func(reason string, reasonNote *string, resumeAtISO *string, scheduleTeardown ScheduleTeardown) error {
			var parsedResumeAt *time.Time
			if resumeAtISO != nil && *resumeAtISO != "" {
				if t, err := time.Parse(time.RFC3339, *resumeAtISO); err == nil {
					parsedResumeAt = &t
				}
			}
			note := ""
			if reasonNote != nil {
				note = *reasonNote
			}
			scheduleTeardown(func() error {
				teardownCli()
				state.safeResolve(AgentOutcome{
					Kind:         OutcomeParkRequested,
					Reason:       reason,
					ReasonNote:   note,
					ResumeAt:     parsedResumeAt,
					SessionToken: opts.SessionID,
				})
				return nil
			})
			return nil
		},
	})

	if disallowed := firstDisallowedExposeEnv(cliConfig.ExposeEnv, opts.ExposeEnvAllowlist); disallowed != "" {
		dispatchMcp.Registry.Release(callbackToken)
		closeDispatchMcp()
		return erroredOutcome("agent/attribute_invalid", map[string]any{
			"reason": fmt.Sprintf(
				`cli.expose_env declares env var %q which is not in the operator allowlist (RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST) — instance %q, node %q`,
				disallowed, opts.InstanceID, opts.NodeID),
			"disallowed_env_var": disallowed,
			"instance_id":        opts.InstanceID,
			"node_id":            opts.NodeID,
		})
	}

	hostServers, hostAllowed, err := resolveHostServers(
		cliConfig.McpServers, opts.McpAllowlist, opts.InstanceID, opts.NodeID, &catalogTeardowns, logger,
	)
	if err != nil {
		tearDownCatalogServers()
		dispatchMcp.Registry.Release(callbackToken)
		closeDispatchMcp()
		payload := map[string]any{"reason": err.Error()}
		var disallowedErr *mcpDisallowedError
		if errors.As(err, &disallowedErr) {
			payload["disallowed_mcp_server"] = disallowedErr.ServerName
			payload["instance_id"] = opts.InstanceID
			payload["node_id"] = opts.NodeID
		}
		return erroredOutcome("agent/attribute_invalid", payload)
	}

	templateAllowed := cliConfig.AllowedTools
	var allowedToolsUnion []string
	if len(templateAllowed) > 0 || len(hostAllowed) > 0 {
		allowedToolsUnion = append(append([]string{}, templateAllowed...), hostAllowed...)
	}

	callbackEnv := map[string]string{
		"RIMSKY_CALLBACK_URL":   dispatchMcp.URL,
		"RIMSKY_CALLBACK_TOKEN": callbackToken,
	}
	tools := append([]CliToolConfig{
		{Kind: CliToolKindMcpHTTP, Name: CallbackMCPServerName, URL: dispatchMcp.URL},
	}, hostServers...)

	var handle CliHandle
	var spawnErr error
	if opts.SessionToken != "" {
		logger.Info("cli.resume_with_session_token", "run_id", opts.SessionID, "session_token", opts.SessionToken)
		handle, spawnErr = opts.CliRunner.Resume(CliResumeRequest{
			SessionID:       opts.SessionToken,
			Prompt:          renderedUser,
			Tools:           tools,
			Model:           opts.Model,
			AllowedTools:    allowedToolsUnion,
			DisallowedTools: cliConfig.DisallowedTools,
			PermissionMode:  cliConfig.PermissionMode,
			Bare:            cliConfig.Bare,
			AddDirs:         cliConfig.AddDirs,
			MaxBudgetUSD:    cliConfig.MaxBudgetUSD,
			Env:             callbackEnv,
			ExposeEnvNames:  cliConfig.ExposeEnv,
			Cwd:             cwd,
		})
	} else {
		handle, spawnErr = opts.CliRunner.Spawn(CliSpawnRequest{
			Model:           opts.Model,
			SystemPrompt:    opts.SystemPrompt,
			UserPrompt:      renderedUser,
			Tools:           tools,
			Env:             callbackEnv,
			ExposeEnvNames:  cliConfig.ExposeEnv,
			Cwd:             cwd,
			SessionID:       opts.SessionID,
			Bare:            cliConfig.Bare,
			PermissionMode:  cliConfig.PermissionMode,
			AllowedTools:    allowedToolsUnion,
			DisallowedTools: cliConfig.DisallowedTools,
			AddDirs:         cliConfig.AddDirs,
			MaxBudgetUSD:    cliConfig.MaxBudgetUSD,
		})
	}
	if spawnErr != nil {
		dispatchMcp.Registry.Release(callbackToken)
		closeDispatchMcp()
		tearDownCatalogServers()
		return erroredOutcome("agent/cli_spawn_failed", map[string]any{"error": spawnErr.Error()})
	}
	state.mu.Lock()
	state.handle = handle
	state.mu.Unlock()
	logger.Info("cli.spawned",
		"run_id", opts.SessionID,
		"pid", handle.Pid(),
		"model", opts.Model,
		"cwd", cwd,
		"bare", cliConfig.Bare,
		"permission_mode", permissionModeOrDefault(cliConfig.PermissionMode),
		"mcp_url", dispatchMcp.URL)

	handleStdoutChunk := func(chunk string, retry bool) {
		state.mu.Lock()
		state.lastStdoutAt = time.Now()
		events := state.streamParser.Push(chunk)
		for _, ev := range events {
			if ev.Kind == "tool_use_start" {
				state.openToolUses[ev.ID] = openToolUse{name: ev.Name, startedAt: state.lastStdoutAt}
			} else {
				delete(state.openToolUses, ev.ID)
			}
		}
		state.mu.Unlock()
		if retry {
			logger.Info("cli.stdout", "run_id", opts.SessionID, "retry", true, "chunk", truncateChunk(chunk))
		} else {
			logger.Info("cli.stdout", "run_id", opts.SessionID, "chunk", truncateChunk(chunk))
		}
	}
	appendStderr := func(chunk string) {
		state.mu.Lock()
		state.stderrBuf += chunk
		if len(state.stderrBuf) > 16*1024 {
			state.stderrBuf = state.stderrBuf[len(state.stderrBuf)-16*1024:]
		}
		state.mu.Unlock()
	}
	handle.OnStdout(func(chunk string) { handleStdoutChunk(chunk, false) })
	handle.OnStderr(func(chunk string) {
		logger.Warn("cli.stderr", "run_id", opts.SessionID, "chunk", truncateChunk(chunk))
		appendStderr(chunk)
	})

	teardownAndResolve := func(outcome AgentOutcome) {
		state.teardownInProgress.Store(true)
		h := state.currentHandle()
		if h != nil {
			h.SendSigterm()
			waitExitWithTimeout(h, 500*time.Millisecond)
			h.SendSigkill()
			waitExitWithTimeout(h, 5*time.Second)
		}
		state.closeTeardownDone()
		state.safeResolve(outcome)
	}

	go func() {
		if silenceTimeoutMs <= 0 && toolUseTimeoutMs <= 0 {
			return
		}
		for !state.resolved.Load() && !state.silenceStopped.Load() {
			time.Sleep(100 * time.Millisecond)
			if state.resolved.Load() || state.silenceStopped.Load() || state.teardownInProgress.Load() {
				return
			}
			now := time.Now()
			state.mu.Lock()
			var oldestID string
			var oldest *openToolUse
			for id, info := range state.openToolUses {
				if oldest == nil || info.startedAt.Before(oldest.startedAt) {
					infoCopy := info
					oldest = &infoCopy
					oldestID = id
				}
			}
			lastStdoutAt := state.lastStdoutAt
			state.mu.Unlock()
			if oldest != nil {
				if toolUseTimeoutMs > 0 && now.Sub(oldest.startedAt) > time.Duration(toolUseTimeoutMs)*time.Millisecond {
					teardownAndResolve(erroredOutcome("agent/tool_use_timeout", map[string]any{
						"tool_use_id": oldestID,
						"tool_name":   oldest.name,
						"duration_ms": now.Sub(oldest.startedAt).Milliseconds(),
					}))
					return
				}
			} else if silenceTimeoutMs > 0 && now.Sub(lastStdoutAt) > time.Duration(silenceTimeoutMs)*time.Millisecond {
				teardownAndResolve(erroredOutcome("agent/timeout", map[string]any{
					"silence_duration_ms": now.Sub(lastStdoutAt).Milliseconds(),
				}))
				return
			}
		}
	}()

	spawnedAt := time.Now()
	go func() {
		result := handle.WaitExit()
		logger.Info("cli.exited",
			"run_id", opts.SessionID,
			"pid", handle.Pid(),
			"exit_code", exitCodeForLog(result),
			"signal", result.Signal,
			"duration_ms", time.Since(spawnedAt).Milliseconds())
		select {
		case <-state.teardownDone:
		case <-time.After(2 * time.Second):
		}
		if state.teardownInProgress.Load() || state.resolved.Load() {
			return
		}

		state.mu.Lock()
		stderrBuf := state.stderrBuf
		state.mu.Unlock()

		handleRateLimits := cliConfig.HandleRateLimits == nil || *cliConfig.HandleRateLimits
		tryResolveRateLimit := func(exit ExitResult, stderr string) bool {
			if exit.ExitCode == nil || *exit.ExitCode == 0 || stderr == "" {
				return false
			}
			signalRL := DetectRateLimit(stderr, time.Now())
			if !signalRL.Detected {
				return false
			}
			if !handleRateLimits {
				logger.Warn("cli.rate_limit_detected; handle_rate_limits=false → emitting agent/rate_limited Error",
					"run_id", opts.SessionID, "exit_code", *exit.ExitCode, "signal", exit.Signal)
				state.safeResolve(erroredOutcome("agent/rate_limited", map[string]any{
					"exitCode":  *exit.ExitCode,
					"signal":    exit.Signal,
					"resume_at": isoOrNil(signalRL.ResumeAt),
				}))
				return true
			}
			logger.Warn("cli.rate_limit_detected; emitting park_requested",
				"run_id", opts.SessionID, "exit_code", *exit.ExitCode, "signal", exit.Signal)
			note := signalRL.Reason
			if note == "" {
				note = "claude cli rate-limit detected; resume_at=" + isoOrIndefinite(signalRL.ResumeAt)
			}
			state.safeResolve(AgentOutcome{
				Kind:         OutcomeParkRequested,
				Reason:       "snooze",
				ReasonNote:   note,
				ResumeAt:     signalRL.ResumeAt,
				SessionToken: opts.SessionID,
			})
			return true
		}
		if tryResolveRateLimit(result, stderrBuf) {
			return
		}

		if result.ExitCode != nil && *result.ExitCode == 0 && result.Signal == "" {
			reminderPrompt := "You exited without calling mcp__rimsky-callback__report_complete. " +
				"Review what you accomplished in this session and call the appropriate " +
				"callback now: report_complete (with changed:true if you applied edits, " +
				"changed:false if you found nothing to change), report_blocked (if " +
				"something prevented you from finishing), or report_error (if you hit " +
				"an unexpected failure). This is REQUIRED — without it rimsky treats " +
				"the dispatch as failed and discards your work."
			logger.Warn("cli.clean_exit_no_report; attempting resume",
				"run_id", opts.SessionID, "exit_code", 0, "duration_ms", time.Since(spawnedAt).Milliseconds())
			retryHandle, retryErr := opts.CliRunner.Resume(CliResumeRequest{
				SessionID:       opts.SessionID,
				Prompt:          reminderPrompt,
				Tools:           tools,
				Model:           opts.Model,
				AllowedTools:    allowedToolsUnion,
				DisallowedTools: cliConfig.DisallowedTools,
				PermissionMode:  cliConfig.PermissionMode,
				Bare:            cliConfig.Bare,
				AddDirs:         cliConfig.AddDirs,
				MaxBudgetUSD:    cliConfig.MaxBudgetUSD,
				Env:             callbackEnv,
				ExposeEnvNames:  cliConfig.ExposeEnv,
				Cwd:             cwd,
			})
			if retryErr != nil {
				logger.Warn("cli.resume_failed", "run_id", opts.SessionID, "error", retryErr.Error())
				state.safeResolve(erroredOutcome("agent/subprocess_exit/before_complete", map[string]any{
					"exitCode":     0,
					"signal":       result.Signal,
					"retry_failed": retryErr.Error(),
				}))
				return
			}
			state.mu.Lock()
			state.handle = retryHandle
			state.streamParser = NewCliStreamParser()
			state.openToolUses = map[string]openToolUse{}
			state.lastStdoutAt = time.Now()
			state.mu.Unlock()
			retryHandle.OnStdout(func(chunk string) { handleStdoutChunk(chunk, true) })
			retryHandle.OnStderr(func(chunk string) {
				logger.Warn("cli.stderr", "run_id", opts.SessionID, "retry", true, "chunk", truncateChunk(chunk))
				appendStderr(chunk)
			})
			retryStartedAt := time.Now()
			retryResult := retryHandle.WaitExit()
			logger.Info("cli.exited",
				"run_id", opts.SessionID,
				"retry", true,
				"pid", retryHandle.Pid(),
				"exit_code", exitCodeForLog(retryResult),
				"signal", retryResult.Signal,
				"duration_ms", time.Since(retryStartedAt).Milliseconds())
			if !state.resolved.Load() {
				select {
				case <-state.teardownDone:
				case <-time.After(2 * time.Second):
				}
				time.Sleep(10 * time.Millisecond)
			}
			if state.resolved.Load() {
				return
			}
			state.mu.Lock()
			retryStderr := state.stderrBuf
			state.mu.Unlock()
			if tryResolveRateLimit(retryResult, retryStderr) {
				return
			}
			state.safeResolve(erroredOutcome("agent/subprocess_exit/before_complete", map[string]any{
				"exitCode":        exitCodeForLog(retryResult),
				"signal":          retryResult.Signal,
				"retry_attempted": true,
			}))
			return
		}

		errorClass := ClassifyAgentError(stderrBuf)
		if errorClass == "" {
			errorClass = "agent/subprocess_exit/before_complete"
		}
		state.safeResolve(erroredOutcome(errorClass, map[string]any{
			"exitCode": exitCodeForLog(result),
			"signal":   result.Signal,
		}))
	}()

	outcome := <-state.outcomeCh
	state.silenceStopped.Store(true)
	state.closeTeardownDone()
	dispatchMcp.Registry.Release(callbackToken)
	closeDispatchMcp()
	tearDownCatalogServers()
	return outcome
}

func permissionModeOrDefault(mode string) string {
	if mode == "" {
		return "bypassPermissions"
	}
	return mode
}

func truncateChunk(chunk string) string {
	if len(chunk) > 2000 {
		return chunk[:2000]
	}
	return chunk
}

func exitCodeForLog(result ExitResult) any {
	if result.ExitCode == nil {
		return nil
	}
	return *result.ExitCode
}

func isoOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func isoOrIndefinite(t *time.Time) string {
	if t == nil {
		return "indefinite"
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func waitExitWithTimeout(h CliHandle, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		h.WaitExit()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func compileAttributesSchema(schema map[string]any) (*jsonschema.Schema, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("attributes-schema.json", bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	return compiler.Compile("attributes-schema.json")
}

func schemaValidationDetail(err error) string {
	var validationErr *jsonschema.ValidationError
	if errors.As(err, &validationErr) {
		raw, marshalErr := json.Marshal(validationErr.DetailedOutput())
		if marshalErr == nil {
			return string(raw)
		}
	}
	return err.Error()
}

type mcpDisallowedError struct {
	ServerName string
	Message    string
}

func (e *mcpDisallowedError) Error() string { return e.Message }

// @decision: claude-agent-cli-expose-env-field
// @decision: claude-agent-env-passthrough-allowlist
func firstDisallowedExposeEnv(declared []string, allowlist Allowlist) string {
	for _, name := range declared {
		if !allowlist.Allows(name) {
			return name
		}
	}
	return ""
}

// @decision: policies-service-side-enforcement
// @decision: claude-agent-cli-mcp-servers-inline-only
func resolveHostServers(
	servers []McpServerInput,
	allowlist Allowlist,
	instanceID string,
	nodeID string,
	teardowns *[]func() error,
	logger *slog.Logger,
) ([]CliToolConfig, []string, error) {
	var tools []CliToolConfig
	var allowedTools []string
	for _, s := range servers {
		if s.Name == "" {
			return nil, nil, &CliConfigError{Message: "cli.mcp_servers entry is missing a name"}
		}
		if !allowlist.Allows(s.Name) {
			return nil, nil, &mcpDisallowedError{
				ServerName: s.Name,
				Message: fmt.Sprintf(
					`cli.mcp_servers declares server %q which is not in the operator allowlist (RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST) — instance %q, node %q`,
					s.Name, instanceID, nodeID),
			}
		}
		switch s.Transport {
		case "http":
			if s.URL == "" {
				return nil, nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers entry %q (http) requires a non-empty url", s.Name)}
			}
			tools = append(tools, CliToolConfig{Kind: CliToolKindMcpHTTP, Name: s.Name, URL: s.URL, Headers: s.Headers})
		case "stdio":
			if s.Command == "" {
				return nil, nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers entry %q (stdio) requires a non-empty command", s.Name)}
			}
			tools = append(tools, CliToolConfig{Kind: CliToolKindMcpStdio, Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env})
		case "module", "http-loopback":
			if s.Module == "" {
				return nil, nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers entry %q (%s) requires a non-empty module specifier", s.Name, s.Transport)}
			}
			url, bearerToken, teardown, err := standUpModuleLoopback(s.Name, s.Module, logger)
			if err != nil {
				return nil, nil, err
			}
			*teardowns = append(*teardowns, teardown)
			tools = append(tools, CliToolConfig{
				Kind:    CliToolKindMcpHTTP,
				Name:    s.Name,
				URL:     url,
				Headers: map[string]string{"Authorization": "Bearer " + bearerToken},
			})
		default:
			return nil, nil, &CliConfigError{Message: fmt.Sprintf(
				"cli.mcp_servers entry %q has unknown transport %q (expected http | stdio | module | http-loopback)", s.Name, s.Transport)}
		}
		allowedTools = append(allowedTools, autoAllow(s.Name, s.AllowedTools)...)
	}
	return tools, allowedTools, nil
}

func autoAllow(name string, allowedTools []string) []string {
	if len(allowedTools) > 0 {
		out := make([]string, 0, len(allowedTools))
		for _, t := range allowedTools {
			out = append(out, "mcp__"+name+"__"+t)
		}
		return out
	}
	return []string{"mcp__" + name}
}

func resolveCwd(claimProducers map[string]any, cwdFromStore string, cwdOverride string) (cwd string, errMessage string) {
	if cwdFromStore != "" {
		handleEntry, ok := claimProducers[cwdFromStore].(map[string]any)
		if !ok {
			return "", fmt.Sprintf("cwd_from_store: no claim_producer handle named %q in ExecuteRequest.claim_producers", cwdFromStore)
		}
		handle, ok := handleEntry["handle"].(map[string]any)
		if !ok {
			return "", fmt.Sprintf("cwd_from_store: store %q has no handle struct", cwdFromStore)
		}
		address, ok := handle["address"].(string)
		if !ok || address == "" {
			return "", fmt.Sprintf("cwd_from_store: store %q address is not a non-empty string (got %T)", cwdFromStore, handle["address"])
		}
		return validateDirectory(address, fmt.Sprintf("cwd_from_store(%s)", cwdFromStore))
	}
	if cwdOverride != "" {
		return validateDirectory(cwdOverride, "cwd")
	}
	return "", ""
}

func validateDirectory(path string, source string) (string, string) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Sprintf("%s: stat %q failed: %s", source, path, err.Error())
	}
	if !st.IsDir() {
		return "", fmt.Sprintf("%s: path %q exists but is not a directory", source, path)
	}
	return path, ""
}
