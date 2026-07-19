// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: spawned-local-services
package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposeRunSpawnedServices_NoLeakAfterExit(t *testing.T) {
	binDir := t.TempDir()
	rimskyBin := filepath.Join(binDir, "rimsky")
	stubBin := filepath.Join(binDir, "stub-executor")
	buildRepoBinary(t, "./cmd/rimsky", rimskyBin)
	buildComposeStubExecutorBinary(t, stubBin)

	work := t.TempDir()
	copyComposeSampleManifest(t, work)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, rimskyBin,
		"compose", "run",
		"--service", fmt.Sprintf("stub=%s", stubBin),
		"./rimsky-compose.yml",
	)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	rc := -1
	if err == nil {
		rc = 0
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			rc = exitErr.ExitCode()
		} else {
			t.Fatalf("rimsky compose run fork-level failure: %v\nstderr:\n%s", err, stderr.String())
		}
	}

	stderrStr := stderr.String()

	if rc != 1 {
		t.Fatalf("expected exit code 1 (any-failure for mixed-outcome manifest); got %d\nstderr:\n%s", rc, stderrStr)
	}

	stubPID := parseSpawnedServicePID(t, stderrStr, "stub")
	if stubPID <= 0 {
		t.Fatalf("could not locate `spawned service` log envelope for name=stub in stderr:\n%s", stderrStr)
	}

	if !strings.Contains(stderrStr, "instance sample-pipeline/ok: success") {
		t.Fatalf("missing 'ok: success' summary — dispatch never reached the spawned stub:\n%s", stderrStr)
	}
	if !strings.Contains(stderrStr, "instance sample-pipeline/oops: failure") {
		t.Fatalf("missing 'oops: failure' summary — failure-leg dispatch never reached the spawned stub:\n%s", stderrStr)
	}

	waitProcessGone(stubPID)
}

func parseSpawnedServicePID(t *testing.T, stderrStr, name string) int {
	t.Helper()
	for _, line := range strings.Split(stderrStr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var env map[string]any
		if jerr := json.Unmarshal([]byte(line), &env); jerr != nil {
			continue
		}
		if env["msg"] != "spawned service" {
			continue
		}
		if envName, _ := env["name"].(string); envName != name {
			continue
		}
		pidVal, ok := env["pid"].(float64)
		if !ok {
			t.Fatalf("`spawned service` envelope missing numeric pid field: %s", line)
		}
		return int(pidVal)
	}
	return 0
}
