// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestListNodes_TagFilter(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := map[string]any{
		"spec": map[string]any{
			"name":    "tag-filter-" + uuid.NewString(),
			"version": "v1",
			"nodes": []map[string]any{
				{
					"type":     "root",
					"executor": "worker",
					"tags":     []string{"setup"},
				},
				{
					"type":       "child",
					"executor":   "worker",
					"tags":       []string{"recurring"},
					"subscribes": []map[string]any{{"node": "root", "type": "terminal/*", "force_upstream_refresh": false}},
				},
			},
		},
	}
	status, out := h.httpJSON(t, "POST", "/v1/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	tplID := out["template_id"].(string)
	status, _ = h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	status, out = h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID := out["instance_id"].(string)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)
	require.Len(t, nodes, 2)
	tagsSeen := map[string]bool{}
	for _, n := range nodes {
		row, _ := n.(map[string]any)
		tags, _ := row["tags"].([]any)
		require.NotNil(t, tags, "every row carries a tags array, got %v", row)
		for _, tg := range tags {
			tagsSeen[tg.(string)] = true
		}
	}
	require.True(t, tagsSeen["setup"])
	require.True(t, tagsSeen["recurring"])

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes?tag=setup", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ = out["nodes"].([]any)
	require.Len(t, nodes, 1)
	row, _ := nodes[0].(map[string]any)
	require.Equal(t, "root", row["node_type"])

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes?tag=nonexistent", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ = out["nodes"].([]any)
	require.Len(t, nodes, 0)
}
