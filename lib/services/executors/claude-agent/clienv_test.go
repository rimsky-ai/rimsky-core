// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"os"
	"testing"
)

func TestBuildCliEnv_APIKeyPath_CleanupRemovesTempDir(t *testing.T) {
	res, err := BuildCliEnv(CliAuthConfig{AnthropicAPIKey: "sk-test-key"})
	if err != nil {
		t.Fatalf("BuildCliEnv: %v", err)
	}
	tmpDir := res.Env["HOME"]
	if tmpDir == "" {
		t.Fatal("BuildCliEnv: Env[HOME] is empty")
	}
	if _, statErr := os.Stat(tmpDir); statErr != nil {
		t.Fatalf("expected tmp dir %q to exist before Cleanup: %v", tmpDir, statErr)
	}
	res.Cleanup()
	if _, statErr := os.Stat(tmpDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected tmp dir %q to be removed after Cleanup, stat err = %v", tmpDir, statErr)
	}
}

func TestBuildCliEnv_MkdirTempFailureLeaksNothing(t *testing.T) {
	realTempDir := os.TempDir()
	before, err := os.ReadDir(realTempDir)
	if err != nil {
		t.Fatalf("read real temp dir %q: %v", realTempDir, err)
	}

	t.Setenv("TMPDIR", "/nonexistent-rimsky-test-tmpdir")
	if _, err := BuildCliEnv(CliAuthConfig{AnthropicAPIKey: "sk-test-key"}); err == nil {
		t.Fatal("BuildCliEnv: expected an error with an unusable TMPDIR, got nil")
	}

	after, err := os.ReadDir(realTempDir)
	if err != nil {
		t.Fatalf("read real temp dir %q: %v", realTempDir, err)
	}
	if len(after) != len(before) {
		t.Fatalf("entries under %q changed (%d -> %d); expected no directory to be created on MkdirTemp failure",
			realTempDir, len(before), len(after))
	}
}
