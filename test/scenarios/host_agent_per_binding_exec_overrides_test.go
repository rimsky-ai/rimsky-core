// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_per_binding_exec_overrides_test.go — end-to-end proof of the
// per-binding exec-override contract (story S-hostagent-per-binding-exec-overrides).
// A late_bind binding may declare per-binding command args, env vars, a
// working directory, and a ready/spawn-timeout override; the host-agent must
// apply them when it exec()s the child:
//   - argv:    exec.Command(path, binding.args...)
//   - env:     os.Environ() + binding.env (binding env wins on collision)
//   - cwd:     binding.cwd when set, else the instance-level cwd
//   - timeout: binding.timeout_seconds bounds the spawn-readiness wait,
//     folded into Spawn.ready_timeout_seconds (and the proxy's own
//     SpawnAck wait) so a binding-specified timeout shorter than the
//     30s global default actually bounds it.
//
// The stub echoes its OWN os.Args[1:], a chosen env var, and os.Getwd() to
// STUBCHILD_EXEC_LOG on each Execute, so the assertions read what the spawned
// process actually saw rather than inferring it from the agent.
//
// RED (current tree): the wire Binding carries only `path`; the proxy's
// bindingSpec parses only Path; spawn.go builds exec.Command(path) with no
// args, env = os.Environ()+RIMSKY_AGENT_PORT, and a single global cwd/timeout.
// So:
//   - the override sub-case logs empty args, an empty env value, and the
//     agent process's cwd (not the per-binding cwd) → the argv/env/cwd
//     assertions FAIL;
//   - the short-timeout sub-case's binding timeout is never parsed, so the
//     agent uses its 30s default ready timeout and the proxy waits 30s for the
//     SpawnAck → spawn_failed does NOT arrive inside the sub-30s budget → the
//     bounded-wait assertion FAILS.
//
// A later GREEN pass parses the override fields on the proxy binding spec,
// carries them on the Spawn frame, and applies them at exec().
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

// execLogLine is the JSON shape the stub appends to STUBCHILD_EXEC_LOG per
// Execute: the child's own os.Args[1:], the value of the env var named by
// STUBCHILD_EXEC_ENV_KEY, and os.Getwd().
type execLogLine struct {
	Args []string `json:"args"`
	Env  string   `json:"env"`
	Cwd  string   `json:"cwd"`
}

// createBindingOverrideInstance creates an instance whose late-bound service
// binding carries the given override fields (any subset). The binding JSON
// keys are the GREEN-side shape the proxy's bindingSpec parses
// (`args`/`env`/`cwd`/`timeout_seconds`); on the current tree they are ignored
// (unknown JSON fields), which is exactly what makes the override assertions
// fail RED.
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

// readExecLog reads STUBCHILD_EXEC_LOG into its parsed lines. An absent file
// yields nil (no Execute has reached the spawned child yet).
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

// waitForExecLog polls STUBCHILD_EXEC_LOG until at least one line appears or
// the timeout elapses, returning the first line. Used to read back what the
// spawned child actually saw on Execute.
func waitForExecLog(t *testing.T, path string, timeout time.Duration) (execLogLine, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if lines := readExecLog(t, path); len(lines) > 0 {
			return lines[0], true
		}
		time.Sleep(75 * time.Millisecond)
	}
	return execLogLine{}, false
}

// TestHostAgentPerBindingExecOverrides proves the per-binding exec overrides
// are applied at exec(), the per-binding timeout bounds the spawn wait, and a
// binding with no overrides still spawns (backward compatible).
func TestHostAgentPerBindingExecOverrides(t *testing.T) {
	// Not parallel: execs real child processes and binds free ports; keep it
	// serial so the port reservations and process reaping stay predictable.

	t.Run("overrides_applied_at_exec", func(t *testing.T) {
		// The exec-log + env-key must be set BEFORE the fixture starts so the
		// agent (and every child it exec()s) inherits them via os.Environ().
		execLog := t.TempDir() + "/stub-exec.log"
		t.Setenv("STUBCHILD_EXEC_LOG", execLog)
		t.Setenv("STUBCHILD_EXEC_ENV_KEY", "CODEGEN_MODE")

		fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

		// A per-binding cwd the agent must chdir the child into. Must exist on
		// disk (exec.Command fails if cmd.Dir does not).
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

		// The dispatch must reach the spawned child; once it does, the stub
		// echoes its argv/env/cwd.
		line, ok := waitForExecLog(t, execLog, 45*time.Second)
		require.True(t, ok, "the late-bound child never logged an Execute (dispatch did not reach it)")

		require.Equal(t, wantArgs, line.Args,
			"the spawned child must run with the per-binding args (exec.Command(path, binding.args...))")
		require.Equal(t, wantEnvVal, line.Env,
			"the spawned child must see the per-binding env var (binding.env layered onto the inherited environment)")
		// os.Getwd() resolves symlinks (e.g. macOS /var → /private/var); compare
		// against the same resolution so the assertion is about the real chdir,
		// not symlink spelling.
		require.Equal(t, evalSymlinks(t, bindingCwd), evalSymlinks(t, line.Cwd),
			"the spawned child must run in the per-binding cwd")
	})

	t.Run("short_binding_timeout_bounds_the_wait", func(t *testing.T) {
		// STUBCHILD_NO_BIND makes the stub never bind its port, so the spawn
		// readiness wait must time out. The binding declares a SHORT timeout
		// (3s) — far below the 30s global default. If the override is applied,
		// spawn_failed arrives within a sub-30s budget; if it is ignored (today)
		// the agent's 30s default ready timeout and the proxy's 30s SpawnAck
		// wait govern, and spawn_failed does NOT arrive inside the budget.
		t.Setenv("STUBCHILD_NO_BIND", "1")

		fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

		tid := fx.deployLateBindTemplate(t, "late-bind-short-timeout")
		iid := createBindingOverrideInstance(t, fx, tid, "ck-short-timeout", fx.stubBinary, map[string]any{
			"timeout_seconds": 3,
		})

		// Budget chosen well below the 30s global default but well above the 3s
		// per-binding override (plus dispatch/proxy overhead): a spawn_failed
		// inside this window proves the SHORT binding timeout bounded the wait,
		// not the global default.
		const boundedBudget = 15 * time.Second
		start := time.Now()
		require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/error/spawn_failed", boundedBudget),
			"spawn_failed must arrive within the SHORT per-binding timeout (%s), not the 30s global default — "+
				"a no-bind child should fail the readiness wait at the binding-specified 3s", boundedBudget)
		require.Less(t, time.Since(start), boundedBudget,
			"the per-binding timeout must bound the spawn wait below the global default")
	})

	t.Run("no_overrides_still_spawns", func(t *testing.T) {
		// Backward compatibility: a binding with no override fields spawns with
		// inherited env, the global cwd, and the global timeout, and the run
		// reaches terminal/success exactly as the unmodified late-bind path.
		fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

		tid := fx.deployLateBindTemplate(t, "late-bind-no-overrides")
		iid := createBindingOverrideInstance(t, fx, tid, "ck-no-overrides", fx.stubBinary, nil)

		worker := fx.h.FindNode(iid, "worker")
		require.NotNil(t, worker, "worker node should exist")

		require.True(t, fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh, 45*time.Second),
			"a binding with no overrides must still spawn and reach fresh (backward compatible)")
		require.True(t, fx.waitForNodeEventKind(t, iid, "terminal/success", 10*time.Second),
			"a binding with no overrides must still reach terminal/success")
	})
}

// evalSymlinks resolves path through any symlinks (e.g. macOS /var →
// /private/var) so a cwd comparison is about the real directory, not its
// symlink spelling. An unresolvable path falls back to the input.
func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}
