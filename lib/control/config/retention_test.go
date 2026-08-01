// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"os"
	"path/filepath"
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
