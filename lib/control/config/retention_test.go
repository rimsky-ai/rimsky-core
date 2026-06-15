// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRetentionDefaultsWhenAbsent asserts that a rimsky.yml with no
// `retention:` block parses with retention ON by default — every sweep gets
// its documented trailing window so the scheduler tick reaps stale rows out
// of the box (E10 was dead because Retention was the zero value, which
// disables every sweep).
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
}

// TestRetentionExplicitValuesHonored asserts each `retention:` key reaches
// the parsed runtime.RetentionConfig, and an explicit zero disables that
// sweep (the pointer-field loader distinguishes absent from zero).
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
`)
	r := cfg.Retention
	if r.RecentFramesKept != 5 {
		t.Fatalf("RecentFramesKept = %d, want 5", r.RecentFramesKept)
	}
	if r.LineageTrailing != time.Hour {
		t.Fatalf("LineageTrailing = %s, want 1h", r.LineageTrailing)
	}
	// @constraint: explicit zero disables the claim-handle retention
	// sweep — it is NOT re-defaulted to 30d.
	if r.ClaimHandlesTrailing != 0 {
		t.Fatalf("ClaimHandlesTrailing = %s, want 0 (explicit disable)", r.ClaimHandlesTrailing)
	}
	if r.MessageIdempotenciesTrailing != 48*time.Hour {
		t.Fatalf("MessageIdempotenciesTrailing = %s, want 48h", r.MessageIdempotenciesTrailing)
	}
}

// TestRetentionNegativeRejected asserts a negative trailing window is a
// startup error rather than a silently-ignored value.
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
