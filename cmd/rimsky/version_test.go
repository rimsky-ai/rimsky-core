// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import "testing"

func TestResolvedVersionPrefersLdflagStamp(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })

	version = "v1.2.3"
	if got := resolvedVersion(); got != "v1.2.3" {
		t.Fatalf("resolvedVersion() = %q, want the ldflag-stamped value v1.2.3", got)
	}
}

func TestResolvedVersionFallsBackToBuildInfoWhenUnstamped(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })

	version = "dev"
	got := resolvedVersion()
	if got == "" {
		t.Fatal("resolvedVersion() returned empty; want a non-empty version string")
	}
	if got != "dev" && got[0] != 'v' {
		t.Fatalf("resolvedVersion() = %q; unstamped build must yield either the dev sentinel or a vX.Y.Z module version", got)
	}
}
