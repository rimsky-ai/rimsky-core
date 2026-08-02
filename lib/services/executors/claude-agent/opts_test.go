// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"os"
	"testing"
)

func TestCredentialsConfigured_TracksLiveStubModeEnvNotAHandConstructedField(t *testing.T) {
	if (Opts{StubMode: true}).CredentialsConfigured() {
		t.Fatal("a hand-constructed Opts{StubMode:true} without RIMSKY_EXECUTOR_STUB_MODE set must not report credentials configured; " +
			"RunAgent's real dispatch gate reads the env var, not the struct field, so the two must not diverge")
	}

	t.Setenv("RIMSKY_EXECUTOR_STUB_MODE", "1")
	if !(Opts{}).CredentialsConfigured() {
		t.Fatal("with RIMSKY_EXECUTOR_STUB_MODE=1 set, CredentialsConfigured() must report true regardless of the struct field")
	}
}

func TestCredentialsConfigured_RealCredentialsWork(t *testing.T) {
	if !(Opts{Auth: CliAuthConfig{AnthropicAPIKey: "key"}}).CredentialsConfigured() {
		t.Fatal("an explicit API key must satisfy CredentialsConfigured()")
	}
}

// @decision: allowlist-defaults-open
func TestAllowlistFromEnv_UnsetIsOpen(t *testing.T) {
	const envName = "RIMSKY_CLAUDE_AGENT_TEST_ALLOWLIST_UNSET"
	if err := os.Unsetenv(envName); err != nil {
		t.Fatal(err)
	}
	al := allowlistFromEnv(envName)
	if !al.Open() {
		t.Fatal("an unset allowlist env var must produce an open allowlist")
	}
	if !al.Allows("arbitrary-reference") {
		t.Fatal("an open allowlist must accept an arbitrary reference")
	}
}

func TestAllowlistFromEnv_SetEmptyIsClosed(t *testing.T) {
	const envName = "RIMSKY_CLAUDE_AGENT_TEST_ALLOWLIST_EMPTY"
	t.Setenv(envName, "")
	al := allowlistFromEnv(envName)
	if al.Open() {
		t.Fatal("a set-but-empty allowlist env var must produce a closed allowlist")
	}
	if al.Allows("arbitrary-reference") {
		t.Fatal("a set-but-empty allowlist must reject every reference")
	}
}

func TestAllowlistFromEnv_SetWithNamesAllowsOnlyThose(t *testing.T) {
	const envName = "RIMSKY_CLAUDE_AGENT_TEST_ALLOWLIST_NAMES"
	t.Setenv(envName, "alpha, beta,gamma")
	al := allowlistFromEnv(envName)
	if al.Open() {
		t.Fatal("a set allowlist env var must not report open")
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !al.Allows(name) {
			t.Fatalf("allowlist parsed from %q must allow declared name %q", envName, name)
		}
	}
	if al.Allows("delta") {
		t.Fatal("allowlist must reject a name not present in the parsed env var")
	}
}

// @decision: operator-env-namespaced-per-service
func TestLoadOptsFromEnv_AllowlistsRideNamespacedEnvVars(t *testing.T) {
	t.Setenv("RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST", "srv-a")
	if err := os.Unsetenv("RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST"); err != nil {
		t.Fatal(err)
	}
	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.McpAllowlist.Open() {
		t.Fatal("RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST=srv-a must produce a closed McpAllowlist")
	}
	if !opts.McpAllowlist.Allows("srv-a") {
		t.Fatal("McpAllowlist must allow the declared name srv-a")
	}
	if !opts.ExposeEnvAllowlist.Open() {
		t.Fatal("unset RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST must produce an open ExposeEnvAllowlist")
	}
}
