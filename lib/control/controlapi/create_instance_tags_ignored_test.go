// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: node

package controlapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCreateInstance_OperatorSuppliedTagsFieldIsIgnored(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeployBody(t, h, validTemplateBody("operator-tags-ignored-"+uuid.NewString()))

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
		"tags":         []string{"operator-injected"},
		"attribute_overrides": map[string]any{
			"by_node": map[string]any{
				"root": map[string]any{
					"tags": []string{"operator-injected-via-overrides"},
				},
			},
		},
	})
	require.Equal(t, http.StatusCreated, status,
		"an unrecognized top-level 'tags' field on the create-instance body must not be rejected; "+
			"createInstanceRequest carries no Tags field so json.Decode silently drops it; body=%v", out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)
	require.NotEmpty(t, nodes)

	for _, n := range nodes {
		m, _ := n.(map[string]any)
		nodeID, _ := m["id"].(string)
		require.NotEmpty(t, nodeID)

		nstatus, nout := h.httpJSON(t, "GET", "/v1/nodes/"+nodeID, nil)
		require.Equal(t, http.StatusOK, nstatus, nout)
		tags, _ := nout["tags"].([]any)
		for _, tag := range tags {
			require.NotEqual(t, "operator-injected", tag,
				"node %s carries an operator-supplied tag — createInstanceRequest's top-level "+
					"'tags' field must have no wired effect on node tags", nodeID)
			require.NotEqual(t, "operator-injected-via-overrides", tag,
				"node %s carries a tag smuggled through attribute_overrides — the userdata_overrides "+
					"path must have no wired effect on node tags", nodeID)
		}
	}
}
