// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
)

type Allowlist struct {
	set   bool
	names map[string]bool
}

func OpenAllowlist() Allowlist {
	return Allowlist{}
}

func NewAllowlist(names []string) Allowlist {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return Allowlist{set: true, names: set}
}

func (a Allowlist) Open() bool { return !a.set }

func (a Allowlist) Allows(name string) bool {
	if !a.set {
		return true
	}
	return a.names[name]
}

// @decision: operator-env-namespaced-per-service
// @decision: allowlist-defaults-open
func allowlistFromEnv(envName string) Allowlist {
	raw, present := os.LookupEnv(envName)
	if !present {
		return OpenAllowlist()
	}
	var names []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			names = append(names, s)
		}
	}
	return NewAllowlist(names)
}

type Opts struct {
	Host                       string
	GrpcPort                   int
	HTTPPort                   int
	SilenceTimeoutMsDefault    int
	ToolUseTimeoutMsDefault    int
	ClaudeBinary               string
	Auth                       CliAuthConfig
	McpAllowlist               Allowlist
	ExposeEnvAllowlist         Allowlist
	ObservabilityHTTPBridgeURL string
	StubMode                   bool
	DeclaredTags               []string
}

func StubModeEnabled() bool {
	return os.Getenv("RIMSKY_EXECUTOR_STUB_MODE") == "1"
}

func LoadOptsFromEnv() (Opts, error) {
	grpcPort, err := intFromEnv("RIMSKY_EXECUTOR_PORT_GRPC", 9090)
	if err != nil {
		return Opts{}, err
	}
	httpPort, err := intFromEnv("RIMSKY_EXECUTOR_PORT_HTTP", 9190)
	if err != nil {
		return Opts{}, err
	}
	silenceMs, err := intFromEnv("RIMSKY_EXECUTOR_SILENCE_MS", 0)
	if err != nil {
		return Opts{}, err
	}
	toolUseMs, err := intFromEnv("RIMSKY_EXECUTOR_TOOL_USE_TIMEOUT_MS", 0)
	if err != nil {
		return Opts{}, err
	}
	grpcPort, err = agentport.Override(grpcPort)
	if err != nil {
		return Opts{}, err
	}
	opts := Opts{
		Host:                    envOr("RIMSKY_EXECUTOR_HOST", "0.0.0.0"),
		GrpcPort:                grpcPort,
		HTTPPort:                httpPort,
		SilenceTimeoutMsDefault: silenceMs,
		ToolUseTimeoutMsDefault: toolUseMs,
		ClaudeBinary:            os.Getenv("RIMSKY_EXECUTOR_CLAUDE_BINARY"),
		Auth: CliAuthConfig{
			AnthropicAPIKey:      os.Getenv("ANTHROPIC_API_KEY"),
			ClaudeCodeOauthToken: os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"),
		},
		McpAllowlist:               allowlistFromEnv("RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST"),
		ExposeEnvAllowlist:         allowlistFromEnv("RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST"),
		ObservabilityHTTPBridgeURL: os.Getenv("RIMSKY_EXECUTOR_OBSERVABILITY_HTTP_BRIDGE_URL"),
		StubMode:                   StubModeEnabled(),
		DeclaredTags:               DeclaredTags(),
	}
	return opts, nil
}

var ErrCredentialsMissing = errors.New("at least one of ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN must be set")

func (o Opts) CredentialsConfigured() bool {
	return StubModeEnabled() || o.Auth.AnthropicAPIKey != "" || o.Auth.ClaudeCodeOauthToken != ""
}

func envOr(name string, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func intFromEnv(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", name, raw)
	}
	return v, nil
}
