// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/tools/env-registry/scan"
)

// @decision: env-var-registry
func TestEveryLiveEnvVarReadIsRegistered(t *testing.T) {
	repoRoot := findRepoRoot(t)

	reads, err := scan.LiveReads(repoRoot)
	if err != nil {
		t.Fatalf("scan live RIMSKY_* reads: %v", err)
	}
	registryPath := filepath.Join(repoRoot, "tools", "env-registry", "registry.md")
	src, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read %s: %v (generate it with `go run ./tools/env-registry`)", registryPath, err)
	}
	registered := map[string]bool{}
	for _, name := range scan.ParseTable(string(src)) {
		registered[name] = true
	}
	if len(registered) == 0 {
		t.Fatalf("registry at %s parsed to zero entries — regenerate with `go run ./tools/env-registry`", registryPath)
	}

	inLive := map[string]bool{}
	for _, r := range reads {
		inLive[r.Name] = true
		if !registered[r.Name] {
			t.Errorf("live code references %s (%s) but the env-var registry does not list it — regenerate with `go run ./tools/env-registry`", r.Name, r.Site)
		}
	}
	for name := range registered {
		if !inLive[name] {
			t.Errorf("env-var registry lists %s but no live code references it — regenerate with `go run ./tools/env-registry`", name)
		}
	}
}
