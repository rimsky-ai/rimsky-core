// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// @concept: anonymous-mode
func TestResolveTargetAgent_HonorsIdentityFileEnvOverride(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "identity.json")
	if err := os.WriteFile(override, []byte(`{"routing_identity":"override-agent"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hostagent.IdentityFileEnvVar, override)

	got := ResolveTargetAgent("", "")
	if got != "override-agent" {
		t.Fatalf("ResolveTargetAgent must read the %s override before the default path; got %q", hostagent.IdentityFileEnvVar, got)
	}
}

func TestResolveTargetAgent_ExplicitBeatsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "identity.json")
	if err := os.WriteFile(override, []byte(`{"routing_identity":"override-agent"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hostagent.IdentityFileEnvVar, override)

	if got := ResolveTargetAgent("explicit-agent", ""); got != "explicit-agent" {
		t.Fatalf("explicit target must beat the identity-file override; got %q", got)
	}
}

func TestResolveTargetAgent_APIKeySuppressesGuess(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "identity.json")
	if err := os.WriteFile(override, []byte(`{"routing_identity":"override-agent"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(hostagent.IdentityFileEnvVar, override)

	if got := ResolveTargetAgent("", "rk_some_api_key"); got != "" {
		t.Fatalf("an authenticated caller must not guess a target agent; got %q", got)
	}
}
