// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
)

// @concept: anonymous-mode
func TestResolveTargetDaemon_HonorsIdentityFileEnvOverride(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "identity.json")
	if err := os.WriteFile(override, []byte(`{"routing_identity":"override-daemon"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hostdaemon.IdentityFileEnvVar, override)

	got := ResolveTargetDaemon("", "")
	if got != "override-daemon" {
		t.Fatalf("ResolveTargetDaemon must read the %s override before the default path; got %q", hostdaemon.IdentityFileEnvVar, got)
	}
}

func TestResolveTargetDaemon_ExplicitBeatsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "identity.json")
	if err := os.WriteFile(override, []byte(`{"routing_identity":"override-daemon"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hostdaemon.IdentityFileEnvVar, override)

	if got := ResolveTargetDaemon("explicit-daemon", ""); got != "explicit-daemon" {
		t.Fatalf("explicit target must beat the identity-file override; got %q", got)
	}
}

func TestResolveTargetDaemon_APIKeySuppressesGuess(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "identity.json")
	if err := os.WriteFile(override, []byte(`{"routing_identity":"override-daemon"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hostdaemon.IdentityFileEnvVar, override)

	if got := ResolveTargetDaemon("", "rk_some_api_key"); got != "" {
		t.Fatalf("an authenticated caller must not guess a target daemon; got %q", got)
	}
}
