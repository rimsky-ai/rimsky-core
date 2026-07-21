// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"testing"
	"time"
)

func TestLoadConfig_RequiresBackendURL(t *testing.T) {
	t.Setenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN", "postgres://example/db")
	t.Setenv("RIMSKY_OPENLINEAGE_BACKEND_URL", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected LoadConfig to require RIMSKY_OPENLINEAGE_BACKEND_URL " +
			"(an unconfigured backend must fail startup, not silently discard every lineage record)")
	}
}

func TestLoadConfig_RejectsNonPostgresRimskyDSN(t *testing.T) {
	t.Setenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN", "file:/var/lib/rimsky/state.db")
	t.Setenv("RIMSKY_OPENLINEAGE_BACKEND_URL", "http://marquez.example/api")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected LoadConfig to reject a non-Postgres RIMSKY_OPENLINEAGE_RIMSKY_DSN " +
			"with a clear error naming the sqlite export gap, rather than failing later inside pgxpool.New")
	}
}

func TestLoadConfig_RejectsNonPostgresStateDSN(t *testing.T) {
	t.Setenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN", "postgres://example/db")
	t.Setenv("RIMSKY_OPENLINEAGE_STATE_DSN", "file:/var/lib/rimsky/state.db")
	t.Setenv("RIMSKY_OPENLINEAGE_BACKEND_URL", "http://marquez.example/api")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected LoadConfig to reject a non-Postgres RIMSKY_OPENLINEAGE_STATE_DSN")
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

func TestLoadConfig_RejectsZeroPollInterval(t *testing.T) {
	t.Setenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN", "postgres://example/db")
	t.Setenv("RIMSKY_OPENLINEAGE_BACKEND_URL", "http://marquez.example/api")
	t.Setenv("RIMSKY_OPENLINEAGE_POLL_INTERVAL", "0s")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: expected an error for a zero poll interval, got nil " +
			"(time.NewTicker panics on a non-positive duration at Run instead of failing config)")
	}
}

func TestLoadConfig_RejectsNegativePollInterval(t *testing.T) {
	t.Setenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN", "postgres://example/db")
	t.Setenv("RIMSKY_OPENLINEAGE_BACKEND_URL", "http://marquez.example/api")
	t.Setenv("RIMSKY_OPENLINEAGE_POLL_INTERVAL", "-5s")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig: expected an error for a negative poll interval, got nil")
	}
}

func TestLoadConfig_AcceptsPositivePollInterval(t *testing.T) {
	t.Setenv("RIMSKY_OPENLINEAGE_RIMSKY_DSN", "postgres://example/db")
	t.Setenv("RIMSKY_OPENLINEAGE_BACKEND_URL", "http://marquez.example/api")
	t.Setenv("RIMSKY_OPENLINEAGE_POLL_INTERVAL", "10s")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.PollInterval != 10*time.Second {
		t.Fatalf("PollInterval = %v, want 10s", cfg.PollInterval)
	}
}
