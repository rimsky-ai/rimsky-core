// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import "testing"

func TestLoadOptsFromEnv_MalformedGRPCPortFailsStartup(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_HTTP_NODE_PORT", "not-a-port")

	if _, err := LoadOptsFromEnv(); err == nil {
		t.Fatal("LoadOptsFromEnv: expected an error for a malformed RIMSKY_EXECUTOR_HTTP_NODE_PORT, got nil " +
			"(silently binding a default port hides an operator typo)")
	}
}

func TestLoadOptsFromEnv_MalformedTimeoutMsFailsStartup(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS", "not-a-number")

	if _, err := LoadOptsFromEnv(); err == nil {
		t.Fatal("LoadOptsFromEnv: expected an error for a malformed RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS, got nil")
	}
}

func TestLoadOptsFromEnv_MalformedMaxBodyBytesFailsStartup(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES", "not-a-number")

	if _, err := LoadOptsFromEnv(); err == nil {
		t.Fatal("LoadOptsFromEnv: expected an error for a malformed RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES, got nil")
	}
}

func TestLoadOptsFromEnv_ValidEnvSucceeds(t *testing.T) {
	t.Setenv("RIMSKY_EXECUTOR_HTTP_NODE_PORT", "9191")
	t.Setenv("RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS", "5000")
	t.Setenv("RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES", "2048")

	opts, err := LoadOptsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptsFromEnv: %v", err)
	}
	if opts.GRPCPort != 9191 {
		t.Fatalf("GRPCPort = %d, want 9191", opts.GRPCPort)
	}
	if opts.TimeoutMs != 5000 {
		t.Fatalf("TimeoutMs = %d, want 5000", opts.TimeoutMs)
	}
	if opts.MaxBodyBytes != 2048 {
		t.Fatalf("MaxBodyBytes = %d, want 2048", opts.MaxBodyBytes)
	}
}
