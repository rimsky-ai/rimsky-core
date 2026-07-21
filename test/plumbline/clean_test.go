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

func TestPlumblineClean(t *testing.T) {
	repoRoot := findRepoRoot(t)
	assertAllChecksActive(t, repoRoot)

	binPath := resolveBinPath(t)
	if binPath == "" {
		t.Skip("Plumbline binary not found; set PLUMBLINE_BIN to the lint script path, " +
			"or CLAUDE_PLUGIN_ROOT to a plugin install whose bin/plumbline resolves")
	}

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

func resolveBinPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("PLUMBLINE_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("PLUMBLINE_BIN=%q does not exist: %v", p, err)
		}
		return p
	}
	if root := os.Getenv("CLAUDE_PLUGIN_ROOT"); root != "" {
		p := filepath.Join(root, "bin", "plumbline")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

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
	required := []string{"comment_hygiene", "citation_resolution"}
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
