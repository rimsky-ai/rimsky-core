// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: script-friendly-outcome
package scenarios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const composeRunSuccessManifestRel = "cmd/rimsky/cli/compose/testdata/sample-manifest/rimsky-compose-success.yml"

const composeRunLiveManifestRel = "cmd/rimsky/cli/compose/testdata/sample-manifest/rimsky-compose-live.yml"

func TestComposeRunExitCodes_ThreeClasses(t *testing.T) {
	binDir := t.TempDir()
	rimskyBin := filepath.Join(binDir, "rimsky")
	stubBin := filepath.Join(binDir, "stub-executor")
	buildRepoBinary(t, "./cmd/rimsky", rimskyBin)
	buildComposeStubExecutorBinary(t, stubBin)

	t.Run("success_exit_0", func(t *testing.T) {
		work := t.TempDir()
		copyComposeSampleManifest(t, work)
		rc, out := runComposeRunBinary(t, rimskyBin, work, []string{
			"--service", "stub=" + stubBin,
			"./" + filepath.Base(composeRunSuccessManifestRel),
		}, 90*time.Second)
		if rc != 0 {
			t.Fatalf("expected exit code 0 for all-success; got %d\nstderr:\n%s", rc, out)
		}
	})

	t.Run("failure_exit_1", func(t *testing.T) {
		work := t.TempDir()
		copyComposeSampleManifest(t, work)
		rc, out := runComposeRunBinary(t, rimskyBin, work, []string{
			"--service", "stub=" + stubBin,
			"./rimsky-compose.yml",
		}, 90*time.Second)
		if rc != 1 {
			t.Fatalf("expected exit code 1 for any-failure; got %d\nstderr:\n%s", rc, out)
		}
	})

	t.Run("timeout_exit_2", func(t *testing.T) {
		work := t.TempDir()
		copyComposeSampleManifest(t, work)
		rc, out := runComposeRunBinary(t, rimskyBin, work, []string{
			"--service", "stub=" + stubBin,
			"--timeout", "1s",
			"./" + filepath.Base(composeRunLiveManifestRel),
		}, 30*time.Second)
		if rc != 2 {
			t.Fatalf("expected exit code 2 for wall-clock-bound; got %d\nstderr:\n%s", rc, out)
		}
	})
}

func runComposeRunBinary(
	t *testing.T,
	rimskyBin string,
	cwd string,
	args []string,
	timeout time.Duration,
) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	fullArgs := append([]string{"compose", "run"}, args...)
	cmd := exec.CommandContext(ctx, rimskyBin, fullArgs...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	combined := stderr.String()
	if so := stdout.String(); so != "" {
		combined = fmt.Sprintf("%s\n[stdout]\n%s", combined, so)
	}
	if err == nil {
		return 0, combined
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), combined
	}
	t.Fatalf("rimsky compose run fork-level failure: %v\noutput:\n%s", err, combined)
	return -1, combined
}
