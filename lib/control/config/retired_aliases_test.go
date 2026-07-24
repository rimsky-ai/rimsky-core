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

var forbiddenRedirectWords = []string{"renamed", "instead", "no longer", "deprecated"}

type retiredConfigKeyCase struct {
	name      string
	body      string
	wantToken string
}

var retiredConfigKeyCases = []retiredConfigKeyCase{
	{
		name: "top-level `stores:` key",
		body: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
stores:
  topics-ring:
    endpoint: topics-ring:9090
    write_semantics_allowed: [sync]
`,
		wantToken: "stores",
	},
	{
		name: "claim_producers `write_semantics:` single-value shortcut",
		body: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  topics-ring:
    endpoint: topics-ring:9090
    write_semantics: sync
`,
		wantToken: "write_semantics",
	},
	{
		name: "claim_producers `write_semantics_envelope:` alias",
		body: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
claim_producers:
  topics-ring:
    endpoint: topics-ring:9090
    write_semantics_envelope: [sync]
`,
		wantToken: "write_semantics_envelope",
	},
	{
		name: "top-level `templates:` key",
		body: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
templates:
  some_option: true
`,
		wantToken: "templates",
	},
	{
		name: "`templates.ref_validation_mode` nested key",
		body: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
templates:
  ref_validation_mode: none
`,
		wantToken: "templates",
	},
	{
		name: "top-level `max_park_duration:` key",
		body: `
persistence:
  driver: sqlite
  sqlite:
    path: /tmp/rimsky.db
max_park_duration:
  snooze: 1h
`,
		wantToken: "max_park_duration",
	},
}

func TestLoadRimskyConfigYAML_RejectsRetiredKeys(t *testing.T) {
	for _, tc := range retiredConfigKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadCfgErr(t, tc.body)
			if err == nil {
				t.Fatalf("expected rejection of retired key, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantToken) {
				t.Fatalf("error does not name the retired key %q: %v", tc.wantToken, err)
			}
			lowerErr := strings.ToLower(err.Error())
			if !strings.Contains(lowerErr, "not found in type") && !strings.Contains(lowerErr, "unknown") {
				t.Fatalf("rejection must use the generic unknown-key shape: %v", err)
			}
			for _, word := range forbiddenRedirectWords {
				if strings.Contains(lowerErr, word) {
					t.Fatalf("pure erasure: rejection must not direct the caller toward a replacement setting (found %q): %v", word, err)
				}
			}
		})
	}
}

func TestLoadRimskyConfigYAML_CurrentSpellingLoads(t *testing.T) {
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
	entry, ok := cfg.ClaimProducers.ClaimProducers["topics-ring"]
	if !ok {
		t.Fatal("expected topics-ring claim-producer entry to load under the current spelling")
	}
	if entry.Endpoint != "topics-ring:9090" {
		t.Fatalf("endpoint = %q, want topics-ring:9090", entry.Endpoint)
	}
}
