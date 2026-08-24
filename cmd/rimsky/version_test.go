// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import "testing"

func TestResolvedVersionPrefersLdflagStamp(t *testing.T) {
	if got := versionOrBuildInfo("v1.2.3"); got != "v1.2.3" {
		t.Fatalf("versionOrBuildInfo(%q) = %q, want the ldflag-stamped value", "v1.2.3", got)
	}
}

func TestResolvedVersionFallsBackToBuildInfoWhenUnstamped(t *testing.T) {
	got := versionOrBuildInfo(unstampedVersion)
	if got == "" {
		t.Fatal("versionOrBuildInfo returned empty; want a non-empty version string")
	}
	if got != unstampedVersion && got[0] != 'v' {
		t.Fatalf("versionOrBuildInfo(%q) = %q; an unstamped build must yield either the dev sentinel or a vX.Y.Z module version",
			unstampedVersion, got)
	}
}
