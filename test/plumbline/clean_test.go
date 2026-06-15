// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package plumbline

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPlumblineClean runs Plumbline's lint binary against the rimsky tree
// and asserts that all three checks are active in the project's
// .plumbline.json and that the lint reports clean.
func TestPlumblineClean(t *testing.T) {
	binPath := resolveBinPath(t)
	if binPath == "" {
		t.Skip("Plumbline binary not found; set PLUMBLINE_BIN to the lint script path, " +
			"or CLAUDE_PLUGIN_ROOT to a plugin install whose bin/plumbline resolves")
	}

	repoRoot := findRepoRoot(t)

	assertAllChecksActive(t, repoRoot)

	cmd := exec.Command("node", binPath, ".")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil || cmd.ProcessState.ExitCode() != 0 {
		const maxBytes = 2000
		snippet := out
		if len(snippet) > maxBytes {
			snippet = snippet[:maxBytes]
		}
		t.Fatalf("plumbline lint reported non-clean exit (code=%d, err=%v).\nOutput (truncated to %d bytes):\n%s",
			cmd.ProcessState.ExitCode(), err, maxBytes, snippet)
	}
}

// resolveBinPath returns the filesystem path to the Plumbline lint script.
// Prefers PLUMBLINE_BIN; falls back to $CLAUDE_PLUGIN_ROOT/bin/plumbline.
// Returns "" when neither path exists on disk.
func resolveBinPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("PLUMBLINE_BIN"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if root := os.Getenv("CLAUDE_PLUGIN_ROOT"); root != "" {
		p := filepath.Join(root, "bin", "plumbline")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// findRepoRoot walks up from this test file's location until it finds a
// directory containing .plumbline.json, and returns that directory.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed; cannot locate test file path")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".plumbline.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate .plumbline.json walking up from %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// assertAllChecksActive parses .plumbline.json at repoRoot and fails the
// test if any of source_validity, blessed_invariant_test_coverage, or
// comment_hygiene is missing or set to false.
func assertAllChecksActive(t *testing.T, repoRoot string) {
	t.Helper()
	cfgPath := filepath.Join(repoRoot, ".plumbline.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}
	var cfg struct {
		Checks map[string]bool `json:"checks"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse %s: %v", cfgPath, err)
	}
	required := []string{"source_validity", "blessed_invariant_test_coverage", "comment_hygiene"}
	for _, name := range required {
		v, present := cfg.Checks[name]
		if !present {
			t.Fatalf(".plumbline.json checks.%s missing; expected true", name)
		}
		if !v {
			t.Fatalf(".plumbline.json checks.%s = false; expected true", name)
		}
	}
}
