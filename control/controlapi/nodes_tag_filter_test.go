// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Tests for the `?tag=` filter on GET /instances/{idOrKey}/nodes and
// the `tags` field on each row's JSON response. Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// Item 4.

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

	// Template with one node carrying a static "setup" tag and another
	// with a "recurring" tag. Two-node minimum reuses the existing
	// validator-friendly shape.
	body := map[string]any{
		"spec": map[string]any{
			"name":                  "tag-filter-" + uuid.NewString(),
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
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
					"subscribes": []map[string]any{{"node": "root", "type": "terminal/*"}},
				},
			},
		},
	}
	status, out := h.httpJSON(t, "POST", "/templates", body)
	require.Equal(t, http.StatusCreated, status, out)
	tplID := out["template_id"].(string)
	status, _ = h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, status)

	status, out = h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID := out["instance_id"].(string)

	// No filter — both rows returned, each with its tags.
	status, out = h.httpJSON(t, "GET", "/instances/"+instID+"/nodes", nil)
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

	// Filter to setup — only one row.
	status, out = h.httpJSON(t, "GET", "/instances/"+instID+"/nodes?tag=setup", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ = out["nodes"].([]any)
	require.Len(t, nodes, 1)
	row, _ := nodes[0].(map[string]any)
	require.Equal(t, "root", row["node_type"])

	// Filter to a non-existent tag — empty.
	status, out = h.httpJSON(t, "GET", "/instances/"+instID+"/nodes?tag=nonexistent", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ = out["nodes"].([]any)
	require.Len(t, nodes, 0)
}
