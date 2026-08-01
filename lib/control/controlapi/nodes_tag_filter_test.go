// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	authoredCount := 0
	tagsSeen := map[string]bool{}
	for _, n := range nodes {
		row, _ := n.(map[string]any)
		nt, _ := row["node_type"].(string)
		tags, _ := row["tags"].([]any)
		require.NotNil(t, tags, "every row carries a tags array, got %v", row)
		if nt != "" {
			authoredCount++
			for _, tg := range tags {
				tagsSeen[tg.(string)] = true
			}
		}
	}
	require.Equal(t, 2, authoredCount, "expected 2 author-declared nodes (plus the implicit empty-type receiver)")
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

func TestListNodes_TagFilter_MultiTagAndSharedTagAndPagination(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	body := map[string]any{
		"spec": map[string]any{
			"name":    "tag-filter-multi-" + uuid.NewString(),
			"version": "v1",
			"nodes": []map[string]any{
				{
					"type":     "alpha",
					"executor": "worker",
					"tags":     []string{"shared", "only-alpha"},
				},
				{
					"type":       "beta",
					"executor":   "worker",
					"tags":       []string{"shared", "only-beta"},
					"subscribes": []map[string]any{{"node": "alpha", "type": "terminal/*", "force_upstream_refresh": false}},
				},
				{
					"type":       "gamma",
					"executor":   "worker",
					"subscribes": []map[string]any{{"node": "alpha", "type": "terminal/*", "force_upstream_refresh": false}},
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

	nodeTypes := func(nodes []any) map[string]bool {
		out := map[string]bool{}
		for _, n := range nodes {
			row, _ := n.(map[string]any)
			nt, _ := row["node_type"].(string)
			out[nt] = true
		}
		return out
	}

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes?tag=shared", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ := out["nodes"].([]any)
	require.Len(t, nodes, 2, "tag=shared must match every node carrying it, regardless of its other tags")
	types := nodeTypes(nodes)
	require.True(t, types["alpha"], "alpha carries the shared tag")
	require.True(t, types["beta"], "beta carries the shared tag")

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes?tag=only-alpha", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ = out["nodes"].([]any)
	require.Len(t, nodes, 1)
	row, _ := nodes[0].(map[string]any)
	require.Equal(t, "alpha", row["node_type"])
	tags, _ := row["tags"].([]any)
	tagSet := map[string]bool{}
	for _, tg := range tags {
		tagSet[tg.(string)] = true
	}
	require.True(t, tagSet["shared"], "alpha's multi-tag membership must include shared alongside only-alpha")
	require.True(t, tagSet["only-alpha"])

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes?tag=only-beta", nil)
	require.Equal(t, http.StatusOK, status, out)
	nodes, _ = out["nodes"].([]any)
	require.Len(t, nodes, 1)
	row, _ = nodes[0].(map[string]any)
	require.Equal(t, "beta", row["node_type"])

	status, page1 := h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes?tag=shared&limit=1", nil)
	require.Equal(t, http.StatusOK, status, page1)
	rows1, _ := page1["nodes"].([]any)
	require.Len(t, rows1, 1, "tag filter combined with limit=1 must still cap the page")
	cursor, _ := page1["next_cursor"].(string)
	require.NotEmpty(t, cursor, "a filtered page with more matches must still surface a cursor")
	seenType := nodeTypes(rows1)

	status, page2 := h.httpJSON(t, "GET", "/v1/instances/"+instID+"/nodes?tag=shared&limit=1&cursor="+cursor, nil)
	require.Equal(t, http.StatusOK, status, page2)
	rows2, _ := page2["nodes"].([]any)
	require.Len(t, rows2, 1, "the cursor-continued page must still respect the tag filter")
	nextType := nodeTypes(rows2)
	for nt := range nextType {
		require.False(t, seenType[nt], "cursor pagination combined with a tag filter must not repeat a row across pages: %s", nt)
	}
}
