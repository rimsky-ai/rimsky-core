// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-inproc-utility-executor proof — registers and dispatches the
// example at `examples/inproc-loop-counter/template.yml` against a
// scenario harness that boots a real supervisor + real Postgres with
// **no operator-configured external executor for loop_counter**.
//
// The harness's executor map (`stub` + `testexec`) does NOT include
// the inproc loop_counter alias. The inproc registry the supervisor
// seeds at startup via `builtin.RegisterAll` (per
// `lib/runtime/executor/builtin/builtins.go`) is the only resolution
// path for `kind: loop_counter` in this test — the same property that
// holds in a production deployment with no operator config.
//
// The example YAML is the human-readable artifact; this test parses
// it from disk and drives the canonical `POST /v1/templates`
// registration + instance-creation + cascade-to-done path so the
// artifact is exercised end-to-end in CI rather than rotting in
// silence.
//
// @story: inproc-utility-executor
package scenarios

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// resolveInprocExampleTemplate locates the example template YAML
// relative to the repo root. test/scenarios/ sits two directories
// below the repo root; the example file is at
// examples/inproc-loop-counter/template.yml.
func resolveInprocExampleTemplate(t *testing.T) string {
	t.Helper()
	// @deliberate: try a path relative to the test binary's working
	// dir (the package directory under `go test`); repo root is two
	// levels up.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	rel := filepath.Join(cwd, "..", "..", "examples", "inproc-loop-counter", "template.yml")
	if _, err := os.Stat(rel); err == nil {
		abs, err := filepath.Abs(rel)
		require.NoError(t, err)
		return abs
	}
	// @deliberate: fall back to one-level-up resolution in case
	// `go test` is invoked from the repo root under a
	// non-package-cwd config.
	rel = filepath.Join(cwd, "..", "examples", "inproc-loop-counter", "template.yml")
	if _, err := os.Stat(rel); err == nil {
		abs, err := filepath.Abs(rel)
		require.NoError(t, err)
		return abs
	}
	t.Fatalf("inproc loop_counter example YAML not found at expected paths (cwd=%s)", cwd)
	return ""
}

func TestInprocUtilityExecutorE2E(t *testing.T) {
	t.Parallel()

	// @constraint: harness with NO ExtraExecutors for loop_counter.
	// The supervisor's startup seeds the inproc registry + resolver
	// alias for the rimsky-bundled builtins via `builtin.RegisterAll`
	// + `BuiltinExecutorAliases`; the control-API seeds the
	// kind-alias map via `RegisterAllKindAliases`. Both surfaces are
	// populated from the same package constants — no operator config
	// needed.
	h := scenario.Start(t, scenario.HarnessOpts{})

	// @constraint: read the example YAML from disk so the test fails
	// loudly if the example artifact disappears or its shape stops
	// parsing.
	templatePath := resolveInprocExampleTemplate(t)
	raw, err := os.ReadFile(templatePath)
	require.NoError(t, err, "read example template")

	var spec node.TemplateSpec
	require.NoError(t, yaml.Unmarshal(raw, &spec),
		"yaml.Unmarshal must parse the example template into a TemplateSpec")
	require.Equal(t, "inproc-loop-counter-demo", spec.Name,
		"sanity: parsed name should match the example")
	require.NotEmpty(t, spec.Nodes, "sanity: example must declare at least one node")
	require.Equal(t, "loop_counter", spec.Nodes[0].Kind,
		"sanity: the example node must declare `kind: loop_counter` so the kind-sugar resolver is exercised")
	require.Empty(t, spec.Nodes[0].Executor,
		"sanity: the example must NOT pre-spell out an executor; the kind-sugar resolver does the resolution")

	// @constraint: register the template via DeployTemplate, the same
	// path the production `POST /v1/templates` HTTP route uses.
	// Without the kind-sugar resolver inside the control-API,
	// registration would fail with `template_validation_failed` —
	// the success here is part of the proof.
	tid := h.DeployTemplate(spec)
	require.NotEmpty(t, tid, "template_id from DeployTemplate must be non-empty")

	iid := h.CreateInstance(tid, "ck-inproc-loop-counter", map[string]any{})

	// @constraint: wait for `event/done` to surface on the events
	// feed. `done` only fires after the loop_counter reaches
	// new_count == max — observable proof that the inproc
	// loop_counter handler ran AND completed under no external
	// executor configuration.
	counter := h.FindNode(iid, "counter")
	require.NotNil(t, counter, "counter node missing from instance")

	require.True(t,
		h.WaitForEventKind(counter.ID, "event/done", 30*time.Second),
		"counter MUST emit `event/done` within the timeout — proves the inproc loop_counter handler is dispatching")

	// @constraint: at terminal, the node row's state is `fresh` and
	// the run terminated cleanly. WaitForNodeState returns false if
	// the state never lands.
	require.True(t,
		h.WaitForNodeState(counter.ID, cascade.NodeStateFresh, 30*time.Second),
		"counter MUST reach fresh — proves the dispatch terminated cleanly without falling through to an unresolved executor")
}
