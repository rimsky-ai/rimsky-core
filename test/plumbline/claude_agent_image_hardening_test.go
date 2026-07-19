// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package plumbline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAgentImageIsWolfiBasedNonrootWithTini(t *testing.T) {
	repoRoot := findRepoRoot(t)
	dockerfileRaw, err := os.ReadFile(filepath.Join(repoRoot, "lib", "services", "executors", "claude-agent", "Dockerfile"))
	if err != nil {
		t.Fatalf("read claude-agent Dockerfile: %v", err)
	}
	dockerfile := string(dockerfileRaw)

	lastFrom := ""
	for _, line := range strings.Split(dockerfile, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "FROM ") {
			lastFrom = trimmed
		}
	}
	if !strings.Contains(lastFrom, "cgr.dev/chainguard/wolfi-base") {
		t.Errorf("final runtime stage FROM = %q, want a cgr.dev/chainguard/wolfi-base base", lastFrom)
	}

	if !strings.Contains(dockerfile, "\nUSER nonroot\n") {
		t.Error("Dockerfile has no `USER nonroot` directive; the runtime stage must not run as root")
	}
	if !strings.Contains(dockerfile, "adduser -D -u 65532 nonroot") {
		t.Error("Dockerfile no longer pins the nonroot user to UID 65532")
	}

	entrypointIdx := strings.Index(dockerfile, "ENTRYPOINT")
	if entrypointIdx < 0 {
		t.Fatal("Dockerfile has no ENTRYPOINT")
	}
	entrypointLine := dockerfile[entrypointIdx:]
	if nl := strings.Index(entrypointLine, "\n"); nl >= 0 {
		entrypointLine = entrypointLine[:nl]
	}
	if !strings.Contains(entrypointLine, "tini") {
		t.Errorf("ENTRYPOINT = %q, want tini as PID 1 to reap orphaned CLI grandchildren", entrypointLine)
	}
}

func TestScanGateEnforcesCriticalHighAndFailsClosed(t *testing.T) {
	repoRoot := findRepoRoot(t)
	makefileRaw, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(makefileRaw)

	scanStart := strings.Index(makefile, "\nscan:")
	if scanStart < 0 {
		t.Fatal("Makefile has no `scan` target")
	}
	nextTargetOffset := strings.Index(makefile[scanStart+1:], "\n\n")
	scanBlock := makefile[scanStart:]
	if nextTargetOffset >= 0 {
		scanBlock = makefile[scanStart : scanStart+1+nextTargetOffset]
	}

	if !strings.Contains(scanBlock, "--only-severity critical,high") {
		t.Error("`scan` target no longer gates on --only-severity critical,high; a relaxed severity floor would silently wave through CVEs")
	}
	if !strings.Contains(scanBlock, "exit 1") {
		t.Error("`scan` target no longer fails closed (no `exit 1` on unaccepted findings)")
	}
	if !strings.Contains(makefile, "rimsky-executor-claude-agent") {
		t.Fatal("Makefile IMAGES set no longer includes rimsky-executor-claude-agent — it would silently drop out of `make scan`")
	}
}
