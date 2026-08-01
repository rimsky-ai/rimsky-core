// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDispatchDefaultsZeroWhenAbsent(t *testing.T) {
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
`)
	d := cfg.Dispatch
	if d.SyncRPCDeadlineDefault != 0 {
		t.Fatalf("SyncRPCDeadlineDefault = %s, want 0", d.SyncRPCDeadlineDefault)
	}
	if d.MaxQuietPeriodDefault != 0 {
		t.Fatalf("MaxQuietPeriodDefault = %s, want 0", d.MaxQuietPeriodDefault)
	}
	if d.MaxRuntimeDefault != 0 {
		t.Fatalf("MaxRuntimeDefault = %s, want 0", d.MaxRuntimeDefault)
	}
}

func TestDispatchDefaultsExplicitValuesHonored(t *testing.T) {
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
dispatch_defaults:
  sync_rpc_deadline: 45s
  max_quiet_period: 10m
  max_runtime: 24h
`)
	d := cfg.Dispatch
	if d.SyncRPCDeadlineDefault != 45*time.Second {
		t.Fatalf("SyncRPCDeadlineDefault = %s, want 45s", d.SyncRPCDeadlineDefault)
	}
	if d.MaxQuietPeriodDefault != 10*time.Minute {
		t.Fatalf("MaxQuietPeriodDefault = %s, want 10m", d.MaxQuietPeriodDefault)
	}
	if d.MaxRuntimeDefault != 24*time.Hour {
		t.Fatalf("MaxRuntimeDefault = %s, want 24h", d.MaxRuntimeDefault)
	}
}

func TestDispatchDefaultsNegativeRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
dispatch_defaults:
  max_runtime: -1h
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadRimskyConfigYAML(path); err == nil {
		t.Fatalf("expected a validation error for negative max_runtime, got nil")
	}
}
