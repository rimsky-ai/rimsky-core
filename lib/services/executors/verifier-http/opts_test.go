// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package verifierhttp

import "testing"

func TestLoadOptsFromEnv_MalformedPortFailsStartup(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_PORT_GRPC", "not-a-port")

	if _, err := LoadOptsFromEnv(); err == nil {
		t.Fatal("LoadOptsFromEnv: expected an error for a malformed port var, got nil " +
			"(silently binding the default port hides an operator typo)")
	}
}

func TestLoadOptsFromEnv_ValidPortWiredThrough(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_PORT_GRPC", "9999")

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.Port != 9999 {
		t.Fatalf("Port = %d, want 9999", opts.Port)
	}
}
