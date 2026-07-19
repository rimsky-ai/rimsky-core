// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func SessionTokenFromScratchBase64(scratch string) string {
	if scratch == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(scratch)
	if err != nil {
		return ""
	}
	return string(decoded)
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

func unwrapClaimProducersJSON(claimProducers map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range claimProducers {
		entry, ok := v.(map[string]any)
		if !ok {
			out[k] = v
			continue
		}
		handle, _ := entry["handle"].(map[string]any)
		if handle == nil {
			handle = map[string]any{}
		}
		out[k] = map[string]any{
			"kind":   entry["kind"],
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

func stringArrayOrNil(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
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
	cli, ok := v.(map[string]any)
	if !ok || len(cli) == 0 {
		return nil, nil
	}
	out := &CliConfig{}
	if bare, ok := cli["bare"].(bool); ok {
		out.Bare = bare
	}
	out.PermissionMode = stringOrEmpty(cli["permission_mode"])
	out.AllowedTools = stringArrayOrNil(cli["allowed_tools"])
	out.DisallowedTools = stringArrayOrNil(cli["disallowed_tools"])
	out.AddDirs = stringArrayOrNil(cli["add_dirs"])
	out.MaxBudgetUSD = stringOrEmpty(cli["max_budget_usd"])
	if hr, ok := cli["handle_rate_limits"].(bool); ok {
		out.HandleRateLimits = &hr
	}
	if msc, ok := numberAsInt(cli["max_schema_corrections"]); ok {
		out.MaxSchemaCorrections = &msc
	}
	servers, err := parseMcpServers(cli["mcp_servers"])
	if err != nil {
		return nil, err
	}
	out.McpServers = servers
	out.ExposeEnv = stringArrayOrNil(cli["expose_env"])
	signoffs, err := parseRequiredSignoffs(cli["required_signoffs"])
	if err != nil {
		return nil, err
	}
	out.RequiredSignoffs = signoffs
	if msa, ok := numberAsInt(cli["max_signoff_attempts"]); ok {
		out.MaxSignoffAttempts = &msa
	}
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

func numberAsInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return int(f), true
}

func nonNegativeIntOrNil(v any, field string) (*int, error) {
	if v == nil {
		return nil, nil
	}
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f != math.Trunc(f) {
		return nil, &CliConfigError{Message: fmt.Sprintf("%s must be a non-negative integer (ms), got %v", field, v)}
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
		entry := McpServerInput{
			Transport:    transport,
			Name:         name,
			AllowedTools: stringArrayOrNil(e["allowed_tools"]),
		}
		switch transport {
		case "http":
			entry.URL = stringOrEmpty(e["url"])
			if entry.URL == "" {
				return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers[%d] (http) requires a non-empty url", i)}
			}
			entry.Headers = stringMapOrNil(e["headers"])
		case "stdio":
			entry.Command = stringOrEmpty(e["command"])
			if entry.Command == "" {
				return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers[%d] (stdio) requires a non-empty command", i)}
			}
			entry.Args = stringArrayOrNil(e["args"])
			entry.Env = stringMapOrNil(e["env"])
		case "module", "http-loopback":
			entry.Module = stringOrEmpty(e["module"])
			if entry.Module == "" {
				return nil, &CliConfigError{Message: fmt.Sprintf("cli.mcp_servers[%d] (%s) requires a non-empty module specifier", i, transport)}
			}
		default:
			return nil, &CliConfigError{Message: fmt.Sprintf(
				"cli.mcp_servers[%d] has unknown transport %q (expected http | stdio | module | http-loopback)", i, transport)}
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
