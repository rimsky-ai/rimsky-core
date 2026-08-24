// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

type execLogLine struct {
	Args []string `json:"args"`
	Env  string   `json:"env"`
	Cwd  string   `json:"cwd"`
}

func createBindingOverrideInstance(
	t *testing.T,
	fx *hostDaemonFixture,
	templateHash, instanceKey, binaryPath string,
	override map[string]any,
) shared.UUID {
	t.Helper()
	binding := map[string]any{"path": binaryPath}
	for k, v := range override {
		binding[k] = v
	}
	bindings := map[string]any{lateBindServiceName: binding}
	return fx.h.CreateInstanceWithServiceBindings(templateHash, instanceKey, fx.adminKey, map[string]any{}, bindings)
}

func readExecLog(t *testing.T, path string) []execLogLine {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var out []execLogLine
	for _, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var line execLogLine
		require.NoError(t, json.Unmarshal([]byte(raw), &line))
		out = append(out, line)
	}
	return out
}

func waitForExecLog(t *testing.T, path string) execLogLine {
	t.Helper()
	var first execLogLine
	awaited.Until(t, "an exec-log line at "+path, func() bool {
		lines := readExecLog(t, path)
		if len(lines) == 0 {
			return false
		}
		first = lines[0]
		return true
	})
	return first
}

func TestHostDaemonPerBindingExecOverrides(t *testing.T) {

	t.Run("overrides_applied_at_exec", func(t *testing.T) {
		execLog := t.TempDir() + "/stub-exec.log"
		t.Setenv("STUBCHILD_EXEC_LOG", execLog)
		t.Setenv("STUBCHILD_EXEC_ENV_KEY", "CODEGEN_MODE")

		fx := newHostDaemonFixture(t, fixtureOpts{withDaemon: true})

		bindingCwd := t.TempDir()

		wantArgs := []string{"--mode", "fast"}
		wantEnvVal := "turbo"

		tid := fx.deployLateBindTemplate(t, "late-bind-overrides")
		iid := createBindingOverrideInstance(t, fx, tid, "ck-overrides", fx.stubBinary, map[string]any{
			"args": wantArgs,
			"env":  map[string]any{"CODEGEN_MODE": wantEnvVal},
			"cwd":  bindingCwd,
		})

		worker := fx.h.FindNode(iid, "worker")
		require.NotNil(t, worker, "worker node should exist")

		line := waitForExecLog(t, execLog)

		require.Equal(t, wantArgs, line.Args,
			"the spawned child must run with the per-binding args (exec.Command(path, binding.args...))")
		require.Equal(t, wantEnvVal, line.Env,
			"the spawned child must see the per-binding env var (binding.env layered onto the inherited environment)")
		require.Equal(t, evalSymlinks(t, bindingCwd), evalSymlinks(t, line.Cwd),
			"the spawned child must run in the per-binding cwd")
	})

	t.Run("short_binding_timeout_bounds_the_wait", func(t *testing.T) {
		t.Setenv("STUBCHILD_NO_BIND", "1")

		fx := newHostDaemonFixture(t, fixtureOpts{withDaemon: true})

		tid := fx.deployLateBindTemplate(t, "late-bind-short-timeout")
		iid := createBindingOverrideInstance(t, fx, tid, "ck-short-timeout", fx.stubBinary, map[string]any{
			"timeout_seconds": 3,
		})

		fx.waitForNodeEventKind(t, iid, "terminal/error/spawn_failed")
		payload := fx.nodeEventPayload(t, iid, "terminal/error/spawn_failed")
		require.Contains(t, fmt.Sprint(payload), "within 3s",
			"the readiness poll must report the per-binding timeout_seconds=3, not the global default; payload=%v", payload)
	})

	t.Run("no_overrides_still_spawns", func(t *testing.T) {
		fx := newHostDaemonFixture(t, fixtureOpts{withDaemon: true})

		tid := fx.deployLateBindTemplate(t, "late-bind-no-overrides")
		iid := createBindingOverrideInstance(t, fx, tid, "ck-no-overrides", fx.stubBinary, nil)

		worker := fx.h.FindNode(iid, "worker")
		require.NotNil(t, worker, "worker node should exist")

		fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)
		fx.waitForNodeEventKind(t, iid, "terminal/success")
	})
}

func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}
