// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"encoding/base64"
	"fmt"
	"math"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func SessionTokenFromScratch(scratch []byte) string {
	return string(scratch)
}

func SessionTokenToScratchBase64(token string) string {
	return base64.StdEncoding.EncodeToString([]byte(token))
}

func OutcomeToCallbackBody(outcome AgentOutcome) map[string]any {
	switch outcome.Kind {
	case OutcomeComplete:
		var summary any
		if outcome.ChangeSummary != nil {
			summary = *outcome.ChangeSummary
		}
		return map[string]any{
			"success": map[string]any{
				"attributes_delta": outcome.AttributesDelta,
				"changed":          outcome.Changed,
				"change_summary":   summary,
			},
		}
	case OutcomeBlocked:
		return map[string]any{
			"error": map[string]any{
				"error_class": "agent/blocked",
				"payload":     map[string]any{"reason": outcome.Reason, "context": outcome.Context},
			},
		}
	case OutcomeParkRequested:
		parkBody := map[string]any{}
		if outcome.ResumeAt != nil {
			parkBody["resume_at"] = outcome.ResumeAt.UTC().Format(time.RFC3339Nano)
		}
		if outcome.SessionToken != "" {
			parkBody["scratch"] = SessionTokenToScratchBase64(outcome.SessionToken)
		}
		return map[string]any{"park": parkBody}
	default:
		return map[string]any{
			"error": map[string]any{
				"error_class": outcome.ErrorClass,
				"payload":     outcome.Payload,
			},
		}
	}
}

func unwrapClaimProducersProto(claimProducers map[string]*genv1.ClaimProducerHandle) map[string]any {
	out := map[string]any{}
	for k, v := range claimProducers {
		if v == nil {
			out[k] = nil
			continue
		}
		handle := map[string]any{}
		if v.GetHandle() != nil {
			handle = v.GetHandle().AsMap()
		}
		out[k] = map[string]any{
			"kind":   v.GetKind(),
			"handle": handle,
		}
	}
	return out
}

func stringOr(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}

func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

func stringOrEmptyStrict(v any, field string) (string, error) {
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", &CliConfigError{Message: fmt.Sprintf("%s must be a string, got %T", field, v)}
	}
	return s, nil
}

func stringArrayOrNil(v any, field string) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, &CliConfigError{Message: fmt.Sprintf("%s must be an array, got %T", field, v)}
	}
	var out []string
	for i, item := range items {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil, &CliConfigError{Message: fmt.Sprintf("%s[%d] must be a non-empty string, got %v", field, i, item)}
		}
		out = append(out, s)
	}
	return out, nil
}

func stringMapOrNil(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, val := range m {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ParseCliConfig(v any) (*CliConfig, error) {
	if v == nil {
		return nil, nil
	}
	cli, ok := v.(map[string]any)
	if !ok {
		return nil, &CliConfigError{Message: fmt.Sprintf("attributes.cli must be an object, got %T", v)}
	}
	if len(cli) == 0 {
		return nil, nil
	}
	out := &CliConfig{}
	if bare, ok := cli["bare"].(bool); ok {
		out.Bare = bare
	}
	permissionMode, err := stringOrEmptyStrict(cli["permission_mode"], "cli.permission_mode")
	if err != nil {
		return nil, err
	}
	out.PermissionMode = permissionMode
	allowedTools, err := stringArrayOrNil(cli["allowed_tools"], "cli.allowed_tools")
	if err != nil {
		return nil, err
	}
	out.AllowedTools = allowedTools
	disallowedTools, err := stringArrayOrNil(cli["disallowed_tools"], "cli.disallowed_tools")
	if err != nil {
		return nil, err
	}
	out.DisallowedTools = disallowedTools
	addDirs, err := stringArrayOrNil(cli["add_dirs"], "cli.add_dirs")
	if err != nil {
		return nil, err
	}
	out.AddDirs = addDirs
	out.MaxBudgetUSD = stringOrEmpty(cli["max_budget_usd"])
	if hr, ok := cli["handle_rate_limits"].(bool); ok {
		out.HandleRateLimits = &hr
	}
	msc, err := nonNegativeIntOrNil(cli["max_schema_corrections"], "cli.max_schema_corrections")
	if err != nil {
		return nil, err
	}
	out.MaxSchemaCorrections = msc
	servers, err := parseMcpServers(cli["mcp_servers"])
	if err != nil {
		return nil, err
	}
	out.McpServers = servers
	exposeEnv, err := stringArrayOrNil(cli["expose_env"], "cli.expose_env")
	if err != nil {
		return nil, err
	}
	out.ExposeEnv = exposeEnv
	signoffs, err := parseRequiredSignoffs(cli["required_signoffs"])
	if err != nil {
		return nil, err
	}
	out.RequiredSignoffs = signoffs
	msa, err := nonNegativeIntOrNil(cli["max_signoff_attempts"], "cli.max_signoff_attempts")
	if err != nil {
		return nil, err
	}
	out.MaxSignoffAttempts = msa
	stm, err := nonNegativeIntOrNil(cli["silence_timeout_ms"], "cli.silence_timeout_ms")
	if err != nil {
		return nil, err
	}
	out.SilenceTimeoutMs = stm
	tutm, err := nonNegativeIntOrNil(cli["tool_use_timeout_ms"], "cli.tool_use_timeout_ms")
	if err != nil {
		return nil, err
	}
	out.ToolUseTimeoutMs = tutm
	return out, nil
}

func nonNegativeIntOrNil(v any, field string) (*int, error) {
	if v == nil {
		return nil, nil
	}
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f != math.Trunc(f) {
		return nil, &CliConfigError{Message: fmt.Sprintf("%s must be a non-negative integer, got %v", field, v)}
	}
	i := int(f)
	return &i, nil
}

func parseMcpServers(v any) ([]McpServerInput, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers must be an array, got %T", v)}
	}
	var out []McpServerInput
	for i, item := range items {
		e, ok := item.(map[string]any)
		if !ok {
			return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers[%d] must be an object", i)}
		}
		if _, hasRef := e["ref"]; hasRef {
			return nil, &CliConfigError{Message: fmt.Sprintf(
				"cli.mcp_servers[%d] uses the retired {ref} shape — declare the server inline with a transport (http | stdio | module)", i)}
		}
		transport := stringOrEmpty(e["transport"])
		name := stringOrEmpty(e["name"])
		if name == "" {
			return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers[%d].name must be a non-empty string", i)}
		}
		allowedTools, err := stringArrayOrNil(e["allowed_tools"], fmt.Sprintf("cli.mcp_servers[%d].allowed_tools", i))
		if err != nil {
			return nil, err
		}
		entry := McpServerInput{
			Transport:    transport,
			Name:         name,
			AllowedTools: allowedTools,
		}
		switch transport {
		case mcpTransportHTTP:
			entry.URL = stringOrEmpty(e["url"])
			if entry.URL == "" {
				return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers[%d] (http) requires a non-empty url", i)}
			}
			entry.Headers = stringMapOrNil(e["headers"])
		case mcpTransportStdio:
			entry.Command = stringOrEmpty(e["command"])
			if entry.Command == "" {
				return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers[%d] (stdio) requires a non-empty command", i)}
			}
			args, err := stringArrayOrNil(e["args"], fmt.Sprintf("cli.mcp_servers[%d].args", i))
			if err != nil {
				return nil, err
			}
			entry.Args = args
			entry.Env = stringMapOrNil(e["env"])
		case mcpTransportModule, mcpTransportHTTPLoopback:
			entry.Module = stringOrEmpty(e["module"])
			if entry.Module == "" {
				return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers[%d] (%s) requires a non-empty module specifier", i, transport)}
			}
			if _, ok := lookupMcpModule(entry.Module); !ok {
				return nil, &CliConfigError{Message: fmt.Sprintf(
					"cli.mcp_servers[%d] (%s) module %q is not a registered MCP module in this binary (register it via claudeagent.RegisterMcpModule before serving)",
					i, transport, entry.Module)}
			}
		default:
			return nil, unknownMcpTransportError(fmt.Sprintf("cli.mcp_servers[%d]", i), transport)
		}
		out = append(out, entry)
	}
	return out, nil
}

func parseRequiredSignoffs(v any) ([]RequiredSignoff, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, &CliConfigError{Message: fmt.Sprintf("cli.required_signoffs must be an array, got %T", v)}
	}
	var out []RequiredSignoff
	for i, item := range items {
		e, ok := item.(map[string]any)
		if !ok {
			return nil, &CliConfigError{Message: fmt.Sprintf("cli.required_signoffs[%d] must be an object", i)}
		}
		publicKey := stringOrEmpty(e["public_key"])
		if publicKey == "" {
			return nil, &CliConfigError{Message: fmt.Sprintf("cli.required_signoffs[%d].public_key must be a non-empty string", i)}
		}
		out = append(out, RequiredSignoff{
			PublicKey: publicKey,
			Path:      stringOrEmpty(e["path"]),
		})
	}
	return out, nil
}
