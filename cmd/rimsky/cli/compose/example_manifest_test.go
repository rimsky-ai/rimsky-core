// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// example_manifest_test.go — pins the shipped examples/compose/ manifest
// so the acceptance gate's input cannot silently rot. Asserts the
// manifest parses + validates clean and that every referenced template
// spec round-trips rimsky's template validator (so `rimsky compose up`
// against the shipped file registers real, valid templates).
package compose

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// repoRoot derives the repository root from this test file's location
// (cmd/rimsky/cli/compose/) via runtime.Caller, so the path holds no
// matter the working directory the test runner uses.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
}

func TestComposeManifestExampleLoads(t *testing.T) {
	manifestPath := filepath.Join(repoRoot(t), "examples", "compose", "rimsky-compose.yml")

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
