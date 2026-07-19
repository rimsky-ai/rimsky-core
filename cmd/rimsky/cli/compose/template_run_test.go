// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolateEndpointEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"RIMSKY_CONTROL_API", "RIMSKY_CONTEXT"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

func writeRunSpec(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "spec.yml")
	if err := os.WriteFile(p, []byte("name: x\nversion: \"1\"\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunTemplateRun_SelfHostAndEndpointMutuallyExclusive(t *testing.T) {
	isolateEndpointEnv(t)
	spec := writeRunSpec(t)
	got := RunTemplateRun(context.Background(), []string{"--self-host", "--endpoint", "http://127.0.0.1:1", spec})
	if got != 2 {
		t.Fatalf("exit %d, want 2", got)
	}
}

func TestRunTemplateRun_SelfHostRejectsTemplateName(t *testing.T) {
	isolateEndpointEnv(t)
	got := RunTemplateRun(context.Background(), []string{"--template", "already-registered"})
	if got != 2 {
		t.Fatalf("exit %d, want 2", got)
	}
}

func TestRunTemplateRun_SelfHostRejectsExplicitKeep(t *testing.T) {
	isolateEndpointEnv(t)
	spec := writeRunSpec(t)
	got := RunTemplateRun(context.Background(), []string{"--keep", spec})
	if got != 2 {
		t.Fatalf("exit %d, want 2", got)
	}
}

// @story: local-orchestrator-zero-config
func TestRunTemplateRun_SelfHostDrivesBundledInProcExecutorToTerminal(t *testing.T) {
	isolateEndpointEnv(t)
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")

	workDir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	specPath := filepath.Join(workDir, "spec.yml")
	spec := `name: bundled-selfhost-run
version: "1"
nodes:
  - type: worker
    executor: http-node
    attributes:
      schema:
        type: object
        properties:
          stub_probe:
            type: boolean
            default: true
          stub:
            type: boolean
            default: false
`
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}

	got := RunTemplateRun(context.Background(), []string{"--timeout", "90s", specPath})
	if got != 0 {
		t.Fatalf("rimsky run (self-host) exited %d, want 0 — the template names no configured executor, so success requires the self-hosted stack to dispatch through the in-proc bundled http-node handler", got)
	}
}

// @story: local-orchestrator-zero-config
func TestRunTemplateRun_SelfHostIgnoresSiblingRimskyYML(t *testing.T) {
	isolateEndpointEnv(t)
	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")

	workDir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	specPath := filepath.Join(workDir, "spec.yml")
	spec := `name: bundled-selfhost-sibling-ignore
version: "1"
nodes:
  - type: worker
    executor: http-node
    attributes:
      schema:
        type: object
        properties:
          stub_probe:
            type: boolean
            default: true
          stub:
            type: boolean
            default: false
`
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}

	siblingPath := filepath.Join(workDir, "rimsky.yml")
	sibling := `named_locks:
  sibling-only-lock:
    limit: 1
`
	if err := os.WriteFile(siblingPath, []byte(sibling), 0o644); err != nil {
		t.Fatal(err)
	}

	got := RunTemplateRun(context.Background(), []string{"--timeout", "90s", specPath})
	if got != 0 {
		t.Fatalf("rimsky run (self-host) exited %d, want 0", got)
	}

	runsRoot := filepath.Join(workDir, ".rimsky", "runs")
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		t.Fatalf("read runs root %q: %v", runsRoot, err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one run dir under %q, got %d", runsRoot, len(entries))
	}
	generated, err := os.ReadFile(filepath.Join(runsRoot, entries[0].Name(), "rimsky.yml"))
	if err != nil {
		t.Fatalf("read generated rimsky.yml: %v", err)
	}
	if strings.Contains(string(generated), "sibling-only-lock") {
		t.Fatalf("self-host's synthetic rimsky.yml picked up the sibling rimsky.yml's named_locks "+
			"block — self-host must be zero-config and never read a sibling rimsky.yml:\n%s", generated)
	}
}

func TestSnapshotAndSetEnvPreservesOperatorServiceEnv(t *testing.T) {
	t.Setenv("RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST", "docs,search")
	t.Setenv("RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST", "GITHUB_TOKEN")
	restore := snapshotAndSetEnv(map[string]string{
		"RIMSKY_PROCESS_ROLE":     "unified",
		"RIMSKY_CONTROL_API_HOST": "127.0.0.1",
	})
	defer restore()
	if got := os.Getenv("RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST"); got != "docs,search" {
		t.Fatalf("operator env clobbered by the ephemeral-run env snapshot: %q", got)
	}
	if got := os.Getenv("RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST"); got != "GITHUB_TOKEN" {
		t.Fatalf("operator env clobbered by the ephemeral-run env snapshot: %q", got)
	}
	if got := os.Getenv("RIMSKY_PROCESS_ROLE"); got != "unified" {
		t.Fatalf("snapshot did not set the unified marker: %q", got)
	}
}

func TestRunTemplateRun_SelfHostOverridesContextEndpoint(t *testing.T) {
	isolateEndpointEnv(t)
	t.Setenv("RIMSKY_CONTROL_API", "http://127.0.0.1:1")
	got := RunTemplateRun(context.Background(), []string{"--self-host", "--template", "x"})
	if got != 2 {
		t.Fatalf("exit %d, want 2 (self-host branch's --template rejection, not a remote dial)", got)
	}
}
