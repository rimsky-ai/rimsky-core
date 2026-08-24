// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostdaemon

import (
	"testing"
	"time"
)

func TestEnvDurationSec_EmptyReturnsDefault(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DURATION_SEC", "")
	got, err := envDurationSec("RIMSKY_TEST_DURATION_SEC", 7*time.Second)
	if err != nil {
		t.Fatalf("envDurationSec: %v", err)
	}
	if got != 7*time.Second {
		t.Fatalf("got %v, want 7s default", got)
	}
}

func TestEnvDurationSec_ValidValue(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DURATION_SEC", "42")
	got, err := envDurationSec("RIMSKY_TEST_DURATION_SEC", 7*time.Second)
	if err != nil {
		t.Fatalf("envDurationSec: %v", err)
	}
	if got != 42*time.Second {
		t.Fatalf("got %v, want 42s", got)
	}
}

func TestEnvDurationSec_RejectsMalformedValue(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DURATION_SEC", "not-a-number")
	_, err := envDurationSec("RIMSKY_TEST_DURATION_SEC", 7*time.Second)
	if err == nil {
		t.Fatal("expected an error for a non-numeric value, got nil")
	}
}

func TestEnvDurationSec_RejectsZero(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DURATION_SEC", "0")
	_, err := envDurationSec("RIMSKY_TEST_DURATION_SEC", 7*time.Second)
	if err == nil {
		t.Fatal("expected an error for a zero value, got nil")
	}
}

func TestEnvDurationSec_RejectsNegative(t *testing.T) {
	t.Setenv("RIMSKY_TEST_DURATION_SEC", "-5")
	_, err := envDurationSec("RIMSKY_TEST_DURATION_SEC", 7*time.Second)
	if err == nil {
		t.Fatal("expected an error for a negative value, got nil")
	}
}

func TestLoadConfigFromEnv_RejectsMalformedHeartbeat(t *testing.T) {
	t.Setenv("RIMSKY_DAEMON_HEARTBEAT_SEC", "not-a-number")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("expected LoadConfigFromEnv to reject a malformed RIMSKY_DAEMON_HEARTBEAT_SEC, got nil error")
	}
}

func TestLoadConfigFromEnv_RejectsMalformedReapGrace(t *testing.T) {
	t.Setenv("RIMSKY_DAEMON_REAP_GRACE_SEC", "0")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("expected LoadConfigFromEnv to reject a zero RIMSKY_DAEMON_REAP_GRACE_SEC, got nil error")
	}
}

// @concept: host-daemon
func TestLoadConfigFromEnv_AllowPathsParsing(t *testing.T) {
	t.Setenv("RIMSKY_DAEMON_ALLOW_PATHS", " /usr/local/bin/* , ,/opt/tools/** ")
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv: %v", err)
	}
	if len(cfg.AllowPaths) != 2 || cfg.AllowPaths[0] != "/usr/local/bin/*" || cfg.AllowPaths[1] != "/opt/tools/**" {
		t.Fatalf("AllowPaths = %v, want trimmed non-empty globs [/usr/local/bin/* /opt/tools/**]", cfg.AllowPaths)
	}
}

func TestLoadConfigFromEnv_AllowPathsUnsetStaysOpen(t *testing.T) {
	t.Setenv("RIMSKY_DAEMON_ALLOW_PATHS", "")
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv: %v", err)
	}
	if len(cfg.AllowPaths) != 0 {
		t.Fatalf("AllowPaths = %v, want empty (unset stays open)", cfg.AllowPaths)
	}
}
