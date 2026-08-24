// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import "runtime/debug"

const unstampedVersion = "dev"

var version = unstampedVersion

func resolvedVersion() string { return versionOrBuildInfo(version) }

func versionOrBuildInfo(stamped string) string {
	if stamped != unstampedVersion {
		return stamped
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return stamped
}
