// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadCfgErr writes body to a temp rimsky.yml and returns the error from
// LoadRimskyConfigYAML (nil on success). Mirror of mustLoadCfg for the
// rejection path.
func loadCfgErr(t *testing.T, body string) error {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rimsky.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadRimskyConfigYAML(path)
	return err
}

// TestLoadRimskyConfigYAML_RejectsRetiredAliases drives the real config
// loader against each retired YAML alias from the 2026-05-12 nomenclature
// resolution and asserts a clear, specific error naming the retired key.
// A valid config (using the current `claim_producers` / `write_semantics_
// allowed` spellings) still loads.
//
// The rejections are load-bearing: a v0 config carried forward would
// silently lose its `stores:` / `write_semantics:` block under a tolerant
// loader rather than erroring, so the loader must reject loudly.
func TestLoadRimskyConfigYAML_RejectsRetiredAliases(t *testing.T) {
	t.Run("retired top-level `stores:` key rejected", func(t *testing.T) {
		body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
stores:
  topics-ring:
    endpoint: topics-ring:9090
    write_semantics_allowed: [sync]
`
		err := loadCfgErr(t, body)
		if err == nil {
			t.Fatal("expected rejection of retired `stores:` key, got nil")
		}
		if !strings.Contains(err.Error(), "stores") {
			t.Fatalf("error does not name the retired `stores` key: %v", err)
		}
		if !strings.Contains(err.Error(), "claim_producers") {
			t.Fatalf("error does not point at the replacement `claim_producers`: %v", err)
		}
	})

	t.Run("retired `write_semantics:` single-value shortcut rejected", func(t *testing.T) {
		body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  topics-ring:
    endpoint: topics-ring:9090
    write_semantics: sync
`
		err := loadCfgErr(t, body)
		if err == nil {
			t.Fatal("expected rejection of retired `write_semantics:` shortcut, got nil")
		}
		if !strings.Contains(err.Error(), "write_semantics") {
			t.Fatalf("error does not name the retired `write_semantics` key: %v", err)
		}
		if !strings.Contains(err.Error(), "write_semantics_allowed") {
			t.Fatalf("error does not point at the replacement `write_semantics_allowed`: %v", err)
		}
	})

	t.Run("retired `write_semantics_envelope:` alias rejected", func(t *testing.T) {
		body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  topics-ring:
    endpoint: topics-ring:9090
    write_semantics_envelope: [sync]
`
		err := loadCfgErr(t, body)
		if err == nil {
			t.Fatal("expected rejection of retired `write_semantics_envelope:` alias, got nil")
		}
		if !strings.Contains(err.Error(), "write_semantics_envelope") {
			t.Fatalf("error does not name the retired `write_semantics_envelope` key: %v", err)
		}
		if !strings.Contains(err.Error(), "write_semantics_allowed") {
			t.Fatalf("error does not point at the replacement `write_semantics_allowed`: %v", err)
		}
	})

	t.Run("current spelling loads", func(t *testing.T) {
		body := `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  topics-ring:
    endpoint: topics-ring:9090
    write_semantics_allowed: [sync]
`
		cfg := mustLoadCfg(t, body)
		entry, ok := cfg.Stores.Stores["topics-ring"]
		if !ok {
			t.Fatal("expected topics-ring claim-producer entry to load under the current spelling")
		}
		if entry.Endpoint != "topics-ring:9090" {
			t.Fatalf("endpoint = %q, want topics-ring:9090", entry.Endpoint)
		}
	})
}
