// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func resolveInprocExampleTemplate(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	rel := filepath.Join(cwd, "..", "..", "examples", "inproc-loop-counter", "template.yml")
	if _, err := os.Stat(rel); err == nil {
		abs, err := filepath.Abs(rel)
		require.NoError(t, err)
		return abs
	}
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

	h := scenario.Start(t, scenario.HarnessOpts{})

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

	tid := h.DeployTemplate(spec)
	require.NotEmpty(t, tid, "template_id from DeployTemplate must be non-empty")

	iid := h.CreateInstance(tid, "ck-inproc-loop-counter", map[string]any{})

	counter := h.FindNode(iid, "counter")
	require.NotNil(t, counter, "counter node missing from instance")

	require.True(t,
		h.WaitForNodeState(counter.ID, cascade.NodeStateFresh, 30*time.Second),
		"counter MUST reach fresh — proves the dispatch terminated cleanly without falling through to an unresolved executor")
}
