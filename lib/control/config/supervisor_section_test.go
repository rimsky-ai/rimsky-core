// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeUnifiedConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rimsky.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const unifiedConfigPersistence = "persistence:\n  driver: sqlite\n  sqlite:\n    path: /tmp/state.db\n"

// @concept: rimsky-yml
// @decision: launch-config-injection
func TestLoadRimskyConfigYAML_SupervisorTuningLivesInTheUnifiedFile(t *testing.T) {
	t.Setenv("SUPERVISOR_TEST_HOST", "expanded-host.example")
	path := writeUnifiedConfig(t, unifiedConfigPersistence+
		"supervisor:\n"+
		"  supervisor_id: sup-1\n"+
		"  concurrency: 8\n"+
		"  liveness_interval_ms: 200\n"+
		"  claim_poll_interval_ms: 75\n"+
		"  callback:\n"+
		"    host: 127.0.0.5\n"+
		"    port: 9999\n"+
		"    advertise_host: ${SUPERVISOR_TEST_HOST}\n"+
		"    advertise_port: 8001\n")

	cfg, err := LoadRimskyConfigYAML(path)
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	sup := cfg.Supervisor
	if sup.SupervisorID != "sup-1" {
		t.Errorf("supervisor.supervisor_id = %q, want sup-1", sup.SupervisorID)
	}
	if sup.Concurrency != 8 {
		t.Errorf("supervisor.concurrency = %d, want 8", sup.Concurrency)
	}
	if sup.LivenessIntervalMs != 200 {
		t.Errorf("supervisor.liveness_interval_ms = %d, want 200", sup.LivenessIntervalMs)
	}
	if sup.ClaimPollIntervalMs != 75 {
		t.Errorf("supervisor.claim_poll_interval_ms = %d, want 75", sup.ClaimPollIntervalMs)
	}
	if sup.Callback.Host != "127.0.0.5" {
		t.Errorf("supervisor.callback.host = %q, want 127.0.0.5", sup.Callback.Host)
	}
	if sup.Callback.Port == nil || *sup.Callback.Port != 9999 {
		t.Errorf("supervisor.callback.port = %v, want 9999", sup.Callback.Port)
	}
	if sup.Callback.AdvertiseHost != "expanded-host.example" {
		t.Errorf("supervisor.callback.advertise_host = %q, want the expanded value", sup.Callback.AdvertiseHost)
	}
	if sup.Callback.AdvertisePort != 8001 {
		t.Errorf("supervisor.callback.advertise_port = %d, want 8001", sup.Callback.AdvertisePort)
	}
}

// @decision: default-port-allocation
func TestLoadRimskyConfigYAML_SupervisorCallbackPortDistinguishesZeroFromAbsent(t *testing.T) {
	explicit := writeUnifiedConfig(t, unifiedConfigPersistence+
		"supervisor:\n  callback:\n    host: 0.0.0.0\n    port: 0\n")
	cfg, err := LoadRimskyConfigYAML(explicit)
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	if cfg.Supervisor.Callback.Port == nil || *cfg.Supervisor.Callback.Port != 0 {
		t.Errorf("an explicit callback.port of 0 = %v, want a pointer to 0 so the operating system assigns one",
			cfg.Supervisor.Callback.Port)
	}

	omitted := writeUnifiedConfig(t, unifiedConfigPersistence+
		"supervisor:\n  callback:\n    host: 0.0.0.0\n")
	cfg, err = LoadRimskyConfigYAML(omitted)
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	if cfg.Supervisor.Callback.Port != nil {
		t.Errorf("an omitted callback.port = %v, want nil so the supervisor takes the core-block default",
			*cfg.Supervisor.Callback.Port)
	}
}

// @concept: rimsky-yml
func TestLoadRimskyConfigYAML_AbsentSupervisorSectionLeavesTheTuningAtItsDefaults(t *testing.T) {
	cfg, err := LoadRimskyConfigYAML(writeUnifiedConfig(t, unifiedConfigPersistence))
	if err != nil {
		t.Fatalf("LoadRimskyConfigYAML: %v", err)
	}
	if cfg.Supervisor != (SupervisorSection{}) {
		t.Errorf("supervisor section with no supervisor: block = %+v, want the zero value", cfg.Supervisor)
	}
}
