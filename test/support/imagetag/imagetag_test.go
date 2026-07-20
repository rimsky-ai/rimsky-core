// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package imagetag

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageRefUsesEnvTagWhenSet(t *testing.T) {
	t.Setenv(EnvVar, "tag-under-test")
	got := Ref("rimsky-all-in-one")
	want := "rimsky-all-in-one:tag-under-test"
	if got != want {
		t.Fatalf("Ref with %s set: got %q, want %q", EnvVar, got, want)
	}
}

func TestSrcTagDeterministicAndContentSensitive(t *testing.T) {
	script := srcTagScriptPath(t)
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	writeTracked(t, repo, "tracked.txt", "one")

	first := srcTagIn(t, script, repo)
	second := srcTagIn(t, script, repo)
	if first != second {
		t.Fatalf("same tree hashed differently: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "src-") || len(first) != len("src-")+12 {
		t.Fatalf("tag %q does not match src-<12-hex-tree-hash>", first)
	}

	writeTracked(t, repo, "tracked.txt", "two")
	changed := srcTagIn(t, script, repo)
	if changed == first {
		t.Fatalf("content change did not move the tag (still %q)", first)
	}

	gitIn(t, repo, "add", "-A")
	committed := srcTagIn(t, script, repo)
	if committed != changed {
		t.Fatalf("staging alone moved the tag: %q vs %q — the tag must hash content, not index state", committed, changed)
	}
}

func srcTagScriptPath(t *testing.T) string {
	t.Helper()
	rootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return filepath.Join(strings.TrimSpace(string(rootOut)), Script)
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func writeTracked(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func srcTagIn(t *testing.T, script, dir string) string {
	t.Helper()
	cmd := exec.Command(script)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("%s in %s: %v\n%s", script, dir, err, stderr)
	}
	return strings.TrimSpace(string(out))
}
