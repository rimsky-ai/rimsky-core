// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type golangciConfig struct {
	Linters struct {
		Enable []string `yaml:"enable"`
	} `yaml:"linters"`
	LintersSettings struct {
		Revive struct {
			Rules []struct {
				Name     string `yaml:"name"`
				Disabled bool   `yaml:"disabled"`
			} `yaml:"rules"`
		} `yaml:"revive"`
		Depguard struct {
			Rules map[string]struct {
				Files []string `yaml:"files"`
				Deny  []struct {
					Pkg string `yaml:"pkg"`
				} `yaml:"deny"`
			} `yaml:"rules"`
		} `yaml:"depguard"`
	} `yaml:"linters-settings"`
}

func loadGolangciConfig(t *testing.T) golangciConfig {
	t.Helper()
	repoRoot := findRepoRoot(t)
	src, err := os.ReadFile(filepath.Join(repoRoot, ".golangci.yml"))
	if err != nil {
		t.Fatalf("read .golangci.yml: %v", err)
	}
	var cfg golangciConfig
	if err := yaml.Unmarshal(src, &cfg); err != nil {
		t.Fatalf("parse .golangci.yml: %v", err)
	}
	return cfg
}

func denyPkgs(t *testing.T, cfg golangciConfig, rule string) []string {
	t.Helper()
	r, ok := cfg.LintersSettings.Depguard.Rules[rule]
	if !ok {
		t.Fatalf("depguard rule %q is missing from .golangci.yml — the import boundary it enforces is no longer checked", rule)
	}
	if len(r.Files) == 0 {
		t.Errorf("depguard rule %q has no files globs — it matches nothing and enforces nothing", rule)
	}
	pkgs := make([]string, 0, len(r.Deny))
	for _, d := range r.Deny {
		pkgs = append(pkgs, d.Pkg)
	}
	return pkgs
}

func assertDenies(t *testing.T, rule string, pkgs []string, wantSubstrings ...string) {
	t.Helper()
	for _, want := range wantSubstrings {
		found := false
		for _, p := range pkgs {
			if strings.Contains(p, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("depguard rule %q no longer denies a package matching %q — the boundary has a hole", rule, want)
		}
	}
}

// @decision: depguard-pgx-isolation
// @decision: depguard-foundation-internal
// @decision: depguard-protocols-purity
// @decision: depguard-foundation-purity
// @decision: depguard-graph-purity
// @decision: depguard-runtime-purity
// @decision: depguard-consumption-isolation
// @decision: layer-ordering
// @decision: config-enforced-fitness-tests
func TestDepguardBoundaryRulesPresentAndShaped(t *testing.T) {
	cfg := loadGolangciConfig(t)

	depguardEnabled := false
	for _, l := range cfg.Linters.Enable {
		if l == "depguard" {
			depguardEnabled = true
		}
	}
	if !depguardEnabled {
		t.Fatalf("depguard is not in linters.enable — none of the import-boundary rules run")
	}

	assertDenies(t, "pgx-isolation", denyPkgs(t, cfg, "pgx-isolation"), "jackc/pgx")
	assertDenies(t, "foundation-internal-isolation", denyPkgs(t, cfg, "foundation-internal-isolation"), "lib/foundation/internal")
	assertDenies(t, "protocols-purity", denyPkgs(t, cfg, "protocols-purity"), "lib/foundation")
	assertDenies(t, "foundation-purity", denyPkgs(t, cfg, "foundation-purity"), "lib/graph", "lib/runtime", "lib/control", "/cmd")
	assertDenies(t, "graph-purity", denyPkgs(t, cfg, "graph-purity"), "lib/runtime", "lib/control", "/cmd")
	assertDenies(t, "runtime-purity", denyPkgs(t, cfg, "runtime-purity"), "lib/control", "/cmd")
	assertDenies(t, "consumption-side-isolation", denyPkgs(t, cfg, "consumption-side-isolation"),
		"lib/foundation", "lib/graph", "lib/runtime", "lib/control", "/cmd")
}

// @decision: revive-no-exported-rule
func TestReviveExportedRuleDisabled(t *testing.T) {
	cfg := loadGolangciConfig(t)
	for _, r := range cfg.LintersSettings.Revive.Rules {
		if r.Name == "exported" {
			if !r.Disabled {
				t.Fatalf("revive's exported rule is enabled — it mandates the doc comments the comment-hygiene discipline forbids")
			}
			return
		}
	}
	t.Fatalf("revive's exported rule is no longer explicitly disabled in .golangci.yml — revive's default would mandate doc comments on every exported symbol")
}
