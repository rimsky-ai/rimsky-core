// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaxParkDurationAcceptsRealReasons(t *testing.T) {
	cfg := mustLoadCfg(t, `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
max_park_duration:
  await_callback: 5s
  snooze: 1h
`)
	if got := cfg.MaxParkDuration["await_callback"]; got != 5*time.Second {
		t.Fatalf("MaxParkDuration[await_callback] = %s, want 5s", got)
	}
	if got := cfg.MaxParkDuration["snooze"]; got != time.Hour {
		t.Fatalf("MaxParkDuration[snooze] = %s, want 1h", got)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
max_park_duration:
  callback_wait: 7h
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadRimskyConfigYAML(path)
	if err == nil {
		t.Fatalf("expected a validation error for retired reason key callback_wait, got nil")
	}
	if !strings.Contains(err.Error(), "callback_wait") {
		t.Fatalf("error %q should name the rejected key callback_wait", err.Error())
	}
	if !strings.Contains(err.Error(), "await_callback") || !strings.Contains(err.Error(), "snooze") {
		t.Fatalf("error %q should list the valid reason keys await_callback, snooze", err.Error())
	}
}
