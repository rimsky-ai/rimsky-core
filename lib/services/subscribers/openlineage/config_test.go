// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import "testing"

func TestLoadConfig_RequiresBackendURL(t *testing.T) {
	t.Setenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN", "postgres://example/db")
	t.Setenv("RIMSKY_OPENLINEAGE_BACKEND_URL", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected LoadConfig to require RIMSKY_OPENLINEAGE_BACKEND_URL " +
			"(an unconfigured backend must fail startup, not silently discard every lineage record)")
	}
}

func TestLoadConfig_AcceptsConfiguredBackendURL(t *testing.T) {
	t.Setenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN", "postgres://example/db")
	t.Setenv("RIMSKY_OPENLINEAGE_BACKEND_URL", "http://marquez.example/api")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BackendURL != "http://marquez.example/api" {
		t.Errorf("BackendURL = %q", cfg.BackendURL)
	}
}
