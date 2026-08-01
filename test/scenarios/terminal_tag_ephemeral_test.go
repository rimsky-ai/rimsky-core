// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: terminal-tag
package scenarios

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTerminalTag_EphemeralNotPersistedIntoNodeState(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").
		Success(map[string]any{}, true, "worker-done").
		AttributesDelta(map[string]any{"foo": "bar"}).
		Tags("secret-tag")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "terminal-tag-ephemeral", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"foo": map[string]any{"type": "string", "default": ""},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-terminal-tag-ephemeral", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	var runTags []string
	h.QueryRowSQL(
		`SELECT tags FROM rimsky_node_runs WHERE node_id = $1 ORDER BY sequence DESC LIMIT 1`,
		[]any{worker.ID}, &runTags,
	)
	require.Empty(t, runTags,
		"terminal tags must not be persisted onto the node_run row; got %+v", runTags)

	var bagJSON []byte
	h.QueryRowSQL(
		`SELECT a.data::text FROM rimsky_node_attributes a
		   JOIN rimsky_node_runs r ON r.id = a.node_run_id
		  WHERE r.node_id = $1 ORDER BY r.sequence DESC LIMIT 1`,
		[]any{worker.ID}, &bagJSON,
	)
	var bag map[string]any
	require.NoError(t, json.Unmarshal(bagJSON, &bag))
	_, hasTags := bag["tags"]
	require.False(t, hasTags,
		"persisted attribute bag must not carry a tags key; tags are emission-scoped, "+
			"attributes are the persisted data channel: %+v", bag)
	require.Equal(t, "bar", bag["foo"],
		"attributes_delta must still land in the persisted bag (sanity check on the fixture)")
}
