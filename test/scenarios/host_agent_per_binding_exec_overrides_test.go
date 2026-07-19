// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type execLogLine struct {
	Args []string `json:"args"`
	Env  string   `json:"env"`
	Cwd  string   `json:"cwd"`
}

func createBindingOverrideInstance(
	t *testing.T,
	fx *hostAgentFixture,
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
	for {
		if lines := readExecLog(t, path); len(lines) > 0 {
			return lines[0]
		}
		time.Sleep(75 * time.Millisecond)
	}
}

func TestHostAgentPerBindingExecOverrides(t *testing.T) {

	t.Run("overrides_applied_at_exec", func(t *testing.T) {
		execLog := t.TempDir() + "/stub-exec.log"
		t.Setenv("STUBCHILD_EXEC_LOG", execLog)
		t.Setenv("STUBCHILD_EXEC_ENV_KEY", "CODEGEN_MODE")

		fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

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

		fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

		tid := fx.deployLateBindTemplate(t, "late-bind-short-timeout")
		iid := createBindingOverrideInstance(t, fx, tid, "ck-short-timeout", fx.stubBinary, map[string]any{
			"timeout_seconds": 3,
		})

		const boundedBudget = 15 * time.Second
		start := time.Now()
		fx.waitForNodeEventKind(t, iid, "terminal/error/spawn_failed")
		require.Less(t, time.Since(start), boundedBudget,
			"the per-binding timeout must bound the spawn wait below the global default")
	})

	t.Run("no_overrides_still_spawns", func(t *testing.T) {
		fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

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
