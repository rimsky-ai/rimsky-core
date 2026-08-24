// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLicensingYAML(t *testing.T, dir, contents string) *licensingConfig {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "licensing.yml"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write licensing.yml: %v", err)
	}
	cfg, err := loadLicensingYAML(dir)
	if err != nil {
		t.Fatalf("loadLicensingYAML: %v", err)
	}
	return cfg
}

func TestClassifyLongestPrefixMatch(t *testing.T) {
	cfg := writeLicensingYAML(t, t.TempDir(), `apache:
  - runtime/service/
agpl:
  - runtime/
exempt:
  - runtime/service/internal/skip/
`)
	cases := []struct {
		path string
		want classification
	}{
		{"runtime/service/client.go", classApache},
		{"runtime/runner.go", classAGPL},
		{"runtime/service/internal/skip/dummy.go", classExempt},
		{"unrelated/path/file.go", classUnknown},
	}
	for _, tc := range cases {
		got := cfg.classify(tc.path)
		if got != tc.want {
			t.Errorf("classify(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestClassifyAGPLOverrideUnderApacheParent(t *testing.T) {
	cfg := writeLicensingYAML(t, t.TempDir(), `apache:
  - graph/example/
agpl:
  - graph/example/agpl/
`)
	if got := cfg.classify("graph/example/spec.go"); got != classApache {
		t.Errorf("parent path want apache, got %v", got)
	}
	if got := cfg.classify("graph/example/agpl/runner.go"); got != classAGPL {
		t.Errorf("AGPL override want agpl, got %v", got)
	}
}

func TestClassifyExemptFile(t *testing.T) {
	cfg := writeLicensingYAML(t, t.TempDir(), `apache:
  - cmd/
agpl: []
exempt:
  - cmd/skip-this/file.go
`)
	if got := cfg.classify("cmd/skip-this/file.go"); got != classExempt {
		t.Errorf("exempt file want exempt, got %v", got)
	}
	if got := cfg.classify("cmd/skip-this/other.go"); got != classApache {
		t.Errorf("sibling want apache, got %v", got)
	}
}

func TestClassifyUnknownPath(t *testing.T) {
	cfg := writeLicensingYAML(t, t.TempDir(), `apache:
  - cmd/
agpl: []
exempt: []
`)
	if got := cfg.classify("random/file.go"); got != classUnknown {
		t.Errorf("default want unknown, got %v", got)
	}
}

func TestClassifyTrailingSlashNormalized(t *testing.T) {
	cfg := writeLicensingYAML(t, t.TempDir(), `apache:
  - cmd/
agpl: []
exempt: []
`)
	if got := cfg.classify("cmd"); got != classApache {
		t.Errorf("bare prefix path want apache, got %v", got)
	}
	if got := cfg.classify("cmd/file.go"); got != classApache {
		t.Errorf("subpath want apache, got %v", got)
	}
}
