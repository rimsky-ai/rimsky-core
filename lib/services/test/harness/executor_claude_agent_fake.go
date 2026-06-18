// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

var fakeClaudeBuildMu sync.Mutex

type ClaudeAgentFakeOptions struct {
	McpCatalogYAML       string
	AllowInline          string
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

	env := map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN":       "dummy-token-for-fake-cli-cross-stack-test",
		"RIMSKY_EXECUTOR_HOST":          "0.0.0.0",
		"RIMSKY_EXECUTOR_PORT_GRPC":     "9090",
		"RIMSKY_EXECUTOR_PORT_HTTP":     "9190",
		"RIMSKY_EXECUTOR_CALLBACK_HOST": "127.0.0.1",
		"RIMSKY_EXECUTOR_SILENCE_MS":    "30000",
	}
	if opts.AllowInline != "" {
		env["RIMSKY_EXECUTOR_MCP_ALLOW_INLINE"] = opts.AllowInline
	}
	for k, v := range opts.ExtraEnv {
		env[k] = v
	}

	files := []testcontainers.ContainerFile{}
	if opts.McpCatalogYAML != "" {
		files = append(files, testcontainers.ContainerFile{
			Reader:            strings.NewReader(opts.McpCatalogYAML),
			ContainerFilePath: "/etc/rimsky/mcp-catalog.yaml",
			FileMode:          0o644,
		})
		env["RIMSKY_EXECUTOR_MCP_CATALOG"] = "/etc/rimsky/mcp-catalog.yaml"
	}
	if opts.SignoffPrivateKeyPEM != "" {
		files = append(files, testcontainers.ContainerFile{
			Reader:            strings.NewReader(opts.SignoffPrivateKeyPEM),
			ContainerFilePath: "/etc/rimsky/fake-claude-signoff-private-key.pem",
			FileMode:          0o644,
		})
	}

	fakeClaudeBuildMu.Lock()
	c, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{
			Context:    repoRoot(),
			Dockerfile: "lib/services/test/scenarios/claude_agent_fake_cli/Dockerfile.fake-claude-agent",
			Repo:       "rimsky-test/claude-agent-fake",
			Tag:        "latest",
			KeepImage:  true,
		}),
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithFiles(files...),
		testcontainers.WithExposedPorts("9090/tcp", "9190/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9090/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	fakeClaudeBuildMu.Unlock()
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
	return alias + ":9090"
}

func DumpClaudeAgentFakeLogsForFailure(t testing.TB, c testcontainers.Container) {
	t.Helper()
	rc, err := c.Logs(context.Background())
	if err != nil {
		t.Logf("harness: cannot read claude-agent-fake logs: %v", err)
		return
	}
	defer rc.Close()
	out := make([]byte, 0, 16384)
	buf := make([]byte, 4096)
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	t.Logf("=== claude-agent-fake container logs ===\n%s\n=== end logs ===", string(out))
}
