// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: inproc-utility-executor
package scenarios

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const inprocLoopCounterTemplateRel = "test/fixtures/inproc-loop-counter/template.yml"

func resolveInprocExampleTemplate(t *testing.T) string {
	t.Helper()
	return repoFilePath(t, inprocLoopCounterTemplateRel)
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

	h.WaitForNodeState(counter.ID, cascade.NodeStateFresh)

	var row *persistence.NodeAttributesRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.NodeAttributes().GetLatestByNode(h.Ctx, counter.ID, h.GetLatestFrameRootRunScopeID(iid), tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "counter attribute row must exist after the loop settles")
	require.EqualValues(t, 3, row.Data["count"],
		"loop_counter must have incremented to max (3) before the `done` tag stopped the re-fire")
}
