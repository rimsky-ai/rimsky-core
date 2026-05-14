// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// util.go — small shared helpers reused across verb implementations
// and (via export) the compose package.
package cli

// TruncHash trims a long content-hash (or any opaque string) to a
// human-friendly prefix capped at 19 visible characters plus an ellipsis.
// Strings already at or below the cap are returned unchanged. Used by
// every CLI table that prints sha256-… hashes and by compose plan
// output to keep step lines from wrapping.
func TruncHash(s string) string {
	if len(s) <= 19 {
		return s
	}
	return s[:19] + "…"
}
