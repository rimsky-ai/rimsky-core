// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRetentionDefaultsWhenAbsent(t *testing.T) {
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
`)
	r := cfg.Retention
	if r.RecentFramesKept != defaultRetentionRecentFramesKept {
		t.Fatalf("RecentFramesKept = %d, want default %d", r.RecentFramesKept, defaultRetentionRecentFramesKept)
	}
	if r.LineageTrailing != defaultRetentionLineageTrailing {
		t.Fatalf("LineageTrailing = %s, want default %s", r.LineageTrailing, defaultRetentionLineageTrailing)
	}
	if r.ClaimHandlesTrailing != defaultRetentionClaimHandlesTrailing {
		t.Fatalf("ClaimHandlesTrailing = %s, want default %s", r.ClaimHandlesTrailing, defaultRetentionClaimHandlesTrailing)
	}
	if r.MessageIdempotenciesTrailing != defaultRetentionMessageIdempotenciesTrailing {
		t.Fatalf("MessageIdempotenciesTrailing = %s, want default %s", r.MessageIdempotenciesTrailing, defaultRetentionMessageIdempotenciesTrailing)
	}
	if r.LifecycleOutboxTrailing != 0 {
		t.Fatalf("LifecycleOutboxTrailing = %s, want 0: rimsky owes every lifecycle event at least once, so it "+
			"drops an undelivered one only when the operator names a window", r.LifecycleOutboxTrailing)
	}
}

func TestRetentionExplicitValuesHonored(t *testing.T) {
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
retention:
  recent_frames_kept: 5
  lineage_trailing: 1h
  claim_handles_trailing: 0s
  message_idempotencies_trailing: 48h
  lifecycle_outbox_trailing: 72h
`)
	r := cfg.Retention
	if r.RecentFramesKept != 5 {
		t.Fatalf("RecentFramesKept = %d, want 5", r.RecentFramesKept)
	}
	if r.LineageTrailing != time.Hour {
		t.Fatalf("LineageTrailing = %s, want 1h", r.LineageTrailing)
	}
	if r.ClaimHandlesTrailing != 0 {
		t.Fatalf("ClaimHandlesTrailing = %s, want 0 (explicit disable)", r.ClaimHandlesTrailing)
	}
	if r.MessageIdempotenciesTrailing != 48*time.Hour {
		t.Fatalf("MessageIdempotenciesTrailing = %s, want 48h", r.MessageIdempotenciesTrailing)
	}
	if r.LifecycleOutboxTrailing != 72*time.Hour {
		t.Fatalf("LifecycleOutboxTrailing = %s, want 72h: an operator who names a window bounds the outbox",
			r.LifecycleOutboxTrailing)
	}
}

func TestRetentionNegativeRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
retention:
  lineage_trailing: -1h
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadRimskyConfigYAML(path); err == nil {
		t.Fatalf("expected a validation error for negative lineage_trailing, got nil")
	}
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func TestRetentionNegativeLifecycleOutboxTrailingRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
retention:
  lifecycle_outbox_trailing: -1h
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadRimskyConfigYAML(path); err == nil {
		t.Fatal("expected a validation error for a negative lifecycle_outbox_trailing, got nil")
	}
}

// @decision: service-delivery-stall-signal
func TestRetentionWindowShorterThanTheStallThresholdIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
service_delivery:
  stall_after: 1h
retention:
  lifecycle_outbox_trailing: 30m
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadRimskyConfigYAML(path)
	if err == nil {
		t.Fatal("a window that discards a row before the stall signal reports it must be refused at load")
	}
	if !strings.Contains(err.Error(), "stall_after") {
		t.Fatalf("the error must name the threshold it conflicts with, got %v", err)
	}
}

// @decision: service-delivery-stall-signal
func TestRetentionWindowWiderThanTheStallThresholdLoads(t *testing.T) {
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
service_delivery:
  stall_after: 1h
retention:
  lifecycle_outbox_trailing: 24h
`)
	if cfg.ServiceDelivery.StallAfter != time.Hour {
		t.Fatalf("StallAfter = %s, want 1h", cfg.ServiceDelivery.StallAfter)
	}
	if cfg.Retention.LifecycleOutboxTrailing != 24*time.Hour {
		t.Fatalf("LifecycleOutboxTrailing = %s, want 24h", cfg.Retention.LifecycleOutboxTrailing)
	}
}
