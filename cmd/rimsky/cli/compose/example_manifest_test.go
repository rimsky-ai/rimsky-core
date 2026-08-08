// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
}

func TestComposeManifestExampleLoads(t *testing.T) {
	manifestPath := filepath.Join(repoRoot(t), "test", "fixtures", "compose", "rimsky-compose.yml")

	m, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest(%s): %v", manifestPath, err)
	}
	if m.Project == "" {
		t.Fatal("manifest project is empty")
	}
	if len(m.Templates) == 0 {
		t.Fatal("manifest declares no templates")
	}

	resolveTemplatePaths(m, manifestPath)

	for i, tref := range m.Templates {
		_, spec, err := ResolveTemplate(tref.Path)
		if err != nil {
			t.Fatalf("templates[%d] (%s): resolve: %v", i, tref.Path, err)
		}
		res := node.ValidateTemplate(&spec, node.RegistryHooks{})
		if !res.Ok() {
			t.Fatalf("templates[%d] (%s): ValidateTemplate failed: %+v", i, tref.Path, res.Errors)
		}
	}
}
