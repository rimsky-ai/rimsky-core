// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const claudeAgentFakeImage = "rimsky-test/claude-agent-fake"

type ClaudeAgentFakeOptions struct {
	McpAllowlist         []string
	ExposeEnvAllowlist   []string
	SignoffPrivateKeyPEM string
	ExtraEnv             map[string]string
}

func StartClaudeAgentFakeOnNetwork(
	ctx context.Context,
	t testing.TB,
	networkName, alias string,
	opts ClaudeAgentFakeOptions,
) (endpoint string) {
	t.Helper()
	uniqueAlias := fmt.Sprintf("%s-%d", alias, nextAliasSuffix())

	env := map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN":    "dummy-token-for-fake-cli-cross-stack-test",
		"RIMSKY_EXECUTOR_HOST":       "0.0.0.0",
		"RIMSKY_EXECUTOR_PORT_GRPC":  "9090",
		"RIMSKY_EXECUTOR_PORT_HTTP":  "9190",
		"RIMSKY_EXECUTOR_SILENCE_MS": "30000",
	}
	if opts.McpAllowlist != nil {
		env["RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST"] = strings.Join(opts.McpAllowlist, ",")
	}
	if opts.ExposeEnvAllowlist != nil {
		env["RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST"] = strings.Join(opts.ExposeEnvAllowlist, ",")
	}
	for k, v := range opts.ExtraEnv {
		env[k] = v
	}

	files := []testcontainers.ContainerFile{}
	if opts.SignoffPrivateKeyPEM != "" {
		files = append(files, testcontainers.ContainerFile{
			Reader:            strings.NewReader(opts.SignoffPrivateKeyPEM),
			ContainerFilePath: "/etc/rimsky/fake-claude-signoff-private-key.pem",
			FileMode:          0o644,
		})
	}

	c, err := runWithRetry(ctx, ImageRef(claudeAgentFakeImage),
		tcnet.WithNetworkName([]string{uniqueAlias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(files...),
		testcontainers.WithExposedPorts("9090/tcp", "9190/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9090/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start claude-agent-fake: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			DumpClaudeAgentFakeLogsForFailure(t, c)
		}
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return uniqueAlias + ":9090"
}

func DumpClaudeAgentFakeLogsForFailure(t testing.TB, c testcontainers.Container) {
	t.Helper()
	dumpLogsForFailure(t, "claude-agent-fake", c)
}
