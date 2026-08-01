// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import "testing"

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
