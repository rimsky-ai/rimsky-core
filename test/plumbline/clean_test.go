// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package plumbline

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// @story: clean-lint
func TestPlumblineClean(t *testing.T) {
	repoRoot := findRepoRoot(t)
	assertAllChecksActive(t, repoRoot)

	binPath := resolveBinPath(t)
	if binPath == "" {
		t.Skip("Plumbline binary not found; set PLUMBLINE_BIN to the lint script path, " +
			"or CLAUDE_PLUGIN_ROOT to a plugin install whose bin/plumbline resolves")
	}

	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node interpreter not found on PATH; cannot execute the plumbline lint binary: " + err.Error())
	}

	cmd := exec.Command(nodePath, binPath, ".")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	const maxBytes = 2000
	snippet := out
	if len(snippet) > maxBytes {
		snippet = snippet[:maxBytes]
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("plumbline lint failed to run node %s %s: %v.\nOutput (truncated to %d bytes):\n%s",
			binPath, repoRoot, err, maxBytes, snippet)
	}
	t.Fatalf("plumbline lint reported non-clean exit (code=%d).\nOutput (truncated to %d bytes):\n%s",
		exitErr.ExitCode(), maxBytes, snippet)
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
		if _, err := os.Stat(filepath.Join(dir, ".ok-plumbline", "config.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate .ok-plumbline/config.json walking up from %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

func assertAllChecksActive(t *testing.T, repoRoot string) {
	t.Helper()
	cfgPath := filepath.Join(repoRoot, ".ok-plumbline", "config.json")
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
	if len(cfg.Checks) == 0 {
		t.Fatalf(".ok-plumbline/config.json checks is empty; expected at least one configured check")
	}
	for name, v := range cfg.Checks {
		if !v {
			t.Fatalf(".ok-plumbline/config.json checks.%s = false; expected true", name)
		}
	}
}
