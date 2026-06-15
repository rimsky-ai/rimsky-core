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

// fakeClaudeBuildMu serializes the testcontainers build of the
// fake-claude-agent image so two parallel sub-tests starting the executor
// at the same time do not race on the fixed image tag. See stubBuildMu
// for the broader pattern.
var fakeClaudeBuildMu sync.Mutex

// ClaudeAgentFakeOptions configures the fake claude-agent executor.
// All fields are optional. Empty values produce the most permissive
// stack: no MCP catalog, no signoff private key, no extra auth env. The
// caller sets exactly what each sub-test needs.
type ClaudeAgentFakeOptions struct {
	// McpCatalogYAML is written into a temp file mounted at
	// /etc/rimsky/mcp-catalog.yaml inside the container, and the env
	// var RIMSKY_EXECUTOR_MCP_CATALOG is set to that path. Empty leaves
	// the catalog unset (no startup catalog, no {ref:} resolution).
	McpCatalogYAML string
	// AllowInline sets RIMSKY_EXECUTOR_MCP_ALLOW_INLINE. The default
	// (empty) preserves the executor's hard default of false — inline
	// mcp_servers entries are rejected at dispatch.
	AllowInline string
	// SignoffPrivateKeyPEM is mounted into the container at the fixed
	// path /etc/rimsky/fake-claude-signoff-private-key.pem (the stub
	// reads from that path, not from env). Mounting via file rather
	// than env is load-bearing: the executor's cli-runner.ts deliberately
	// scrubs the parent process.env from the spawned CLI subprocess to
	// keep unrelated pod env out of the CLI; a host env on the
	// executor container would not reach the stub.
	SignoffPrivateKeyPEM string
	// ExtraEnv is layered on top of the executor's base env. Used to
	// thread scenario-specific secrets (e.g. VALIDATOR_TOKEN for the
	// env-ref resolution clause — the executor's env-refs.ts resolves
	// ${env:VAR} against its OWN process.env at spawn, NOT the
	// subprocess env, so VALIDATOR_TOKEN here flows correctly into the
	// resolved Authorization header without leaking into the stub's
	// env).
	ExtraEnv map[string]string
}

// StartClaudeAgentFakeOnNetwork builds (on first use) and starts the
// fake claude-agent executor on the given docker network with the given
// alias. The image is the production claude-agent image with a stub
// `claude` binary baked in and RIMSKY_EXECUTOR_CLAUDE_BINARY pointing at
// it — so every dispatch spawns the stub, but the executor's gRPC server,
// internal MCP callback, signoff gate, attributes_set writeback path,
// async-callback dispatcher, and rimsky-side supervisor are all real.
//
// rimsky eager-dials its declared executors for a Capabilities handshake
// at startup, so this helper MUST be invoked BEFORE BringUpRimsky on the
// same network. Cleanup is registered via t.Cleanup. Fails hard
// (t.Fatal) when the build or container start errors — the harness
// never t.Skip's.
//
// Returns the in-network endpoint ("<alias>:9090") that callers pass to
// BringUpRimsky's WithExecutor option.
func StartClaudeAgentFakeOnNetwork(
	ctx context.Context,
	t testing.TB,
	networkName, alias string,
	opts ClaudeAgentFakeOptions,
) (endpoint string) {
	t.Helper()

	// @constraint: the claude-agent executor's main.ts hard-requires one of
	// ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN at startup unless stub-mode
	// is on (and stub-mode would short-circuit runAgent and defeat the
	// cross-stack proof). Pass a dummy OAuth token so the auth gate is
	// satisfied; the stub binary ignores it.
	env := map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN":   "dummy-token-for-fake-cli-cross-stack-test",
		"RIMSKY_EXECUTOR_HOST":      "0.0.0.0",
		"RIMSKY_EXECUTOR_PORT_GRPC": "9090",
		"RIMSKY_EXECUTOR_PORT_HTTP": "9190",
		// @deliberate: loopback is the callback host the executor advertises in
		// the internal MCP URL the spawned CLI dials. The stub CLI runs as a
		// child of the executor process inside the same container, so loopback
		// is correct.
		"RIMSKY_EXECUTOR_CALLBACK_HOST": "127.0.0.1",
		// @deliberate: the stub CLI's stream-json output is one line; the
		// production 120s default would amplify any inadvertent hang. 30s is
		// enough for the MCP exchange + the gate's retry budget under load.
		"RIMSKY_EXECUTOR_SILENCE_MS": "30000",
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
		// @deliberate: dump the executor container's logs on test failure so
		// the cross-stack proof's "the dispatch hung" / "the stub crashed"
		// failure modes are diagnosable without re-running with manual docker
		// logs. Inlined here (rather than at every call site) so every test
		// using the fake executor benefits.
		if t.Failed() {
			DumpClaudeAgentFakeLogsForFailure(t, c)
		}
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return alias + ":9090"
}

// DumpClaudeAgentFakeLogsForFailure surfaces the fake claude-agent
// container's stdout/stderr on test failure. The executor logs every
// dispatch's spawn / stdout chunks / exit code and the stub CLI's
// stderr lines, so a failing dispatch path (e.g. "stub crashed at MCP
// initialize") becomes visible without manual docker logs.
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
