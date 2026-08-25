// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func seedKeyedInstance(t *testing.T, h *harness) (id string, key string) {
	t.Helper()
	suffix := uuid.NewString()
	_, id = seedBPInstance(t, h, suffix)
	return id, "bp-ck-" + suffix
}

// @concept: instance
func TestEveryInstanceScopedRouteTakesTheInstanceKey(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	id, key := seedKeyedInstance(t, h)

	for _, path := range []string{"/frames", "/messages", "/assets", "/nodes", "/breakpoints"} {
		byID, _ := h.httpJSON(t, "GET", "/v1/instances/"+id+path, nil)
		byKey, body := h.httpJSON(t, "GET", "/v1/instances/"+key+path, nil)
		require.Equal(t, http.StatusOK, byID, path)
		require.Equal(t, byID, byKey,
			"GET /v1/instances/{idOrKey}%s answers the same by key as by id; body=%v", path, body)
	}

	sent := h.httpJSONWithHeaders(t, "POST", "/v1/instances/"+key+"/messages",
		map[string]any{"type": "system/invalidate"},
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusCreated, sent.status, sent.body)

	overridden, body := h.httpJSON(t, "POST", "/v1/instances/"+key+"/debug/override", map[string]any{
		"action": "invalidate_node", "node_type": "root",
	})
	require.Equal(t, http.StatusConflict, overridden,
		"a debug override on a live instance reaches the debuggable-state gate rather than a 404; body=%v", body)
}

// @concept: instance
func TestEveryInstanceScopedRouteAnswers404ForAnUnknownIdentifier(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	for _, unknown := range []string{uuid.NewString(), "no-such-instance-key"} {
		for _, path := range []string{"/frames", "/messages", "/assets", "/nodes", "/breakpoints"} {
			status, body := h.httpJSON(t, "GET", "/v1/instances/"+unknown+path, nil)
			require.Equal(t, http.StatusNotFound, status, "GET %s%s: %v", unknown, path, body)
		}
		sent := h.httpJSONWithHeaders(t, "POST", "/v1/instances/"+unknown+"/messages",
			map[string]any{"type": "system/invalidate"},
			map[string]string{"Idempotency-Key": uuid.NewString()})
		require.Equal(t, http.StatusNotFound, sent.status, sent.body)

		status, body := h.httpJSON(t, "POST", "/v1/instances/"+unknown+"/debug/override", map[string]any{
			"action": "invalidate_node", "node_type": "root",
		})
		require.Equal(t, http.StatusNotFound, status, body)

		status, body = h.httpJSON(t, "GET", "/v1/instances/"+unknown+"/assets/root.data", nil)
		require.Equal(t, http.StatusNotFound, status, body)
	}
}

func TestEveryCollectionRouteRefusesAMalformedLimit(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	id, _ := seedKeyedInstance(t, h)
	handleID := uuid.NewString()

	paths := []string{
		"/v1/instances",
		"/v1/templates",
		"/v1/tags",
		"/v1/events",
		"/v1/audit",
		"/v1/instances/" + id + "/nodes",
		"/v1/instances/" + id + "/frames",
		"/v1/instances/" + id + "/messages",
		"/v1/instances/" + id + "/assets",
		"/v1/instances/" + id + "/breakpoints",
		"/v1/instances/" + id + "/breakpoint-hits",
		"/v1/claim-handles/" + handleID + "/holders",
		"/v1/auth/keys",
		"/v1/admin/diagnostics/held-frames",
		"/v1/admin/diagnostics/parked-nodes",
		"/v1/admin/diagnostics/producer-outbox",
		"/v1/admin/diagnostics/lifecycle-outbox",
		"/v1/admin/diagnostics/wait-sets?frame=" + uuid.NewString() + "&",
		"/v1/lineage/runs/" + uuid.NewString() + "/ancestors",
		"/v1/lineage/runs/" + uuid.NewString() + "/descendants",
		"/v1/lineage/claims/" + handleID + "/ancestors",
		"/v1/lineage/claims/" + handleID + "/descendants",
		"/v1/lineage/by-source/run/" + uuid.NewString(),
		"/v1/lineage/by-producer/some-producer",
	}
	for _, path := range paths {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = ""
		}
		for _, bad := range []string{"limit=not-a-number", "limit=-1", "limit=0"} {
			status, body := h.httpJSON(t, "GET", path+sep+bad, nil)
			require.Equal(t, http.StatusBadRequest, status,
				"GET %s %s must answer 400 rather than silently falling back to the default; body=%v",
				path, bad, body)
		}
	}
}

func TestACollectionRouteClampsAnOversizedLimitToItsCeiling(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	id, _ := seedKeyedInstance(t, h)
	for i := 0; i < parseLimitMax+1; i++ {
		status, out := h.httpJSON(t, "POST", "/v1/instances/"+id+"/breakpoints", map[string]any{
			"checkpoint":  "before_dispatch",
			"matcher":     map[string]any{"node_type": "root"},
			"ttl_seconds": 600,
		})
		require.Equal(t, http.StatusCreated, status, out)
	}

	status, body := h.httpJSON(t, "GET", "/v1/instances/"+id+"/breakpoints?limit=99999999", nil)
	require.Equal(t, http.StatusOK, status,
		"a limit above the ceiling is served at the ceiling rather than refused; body=%v", body)
	require.Len(t, body["breakpoints"], parseLimitMax,
		"the route serves the ceiling, never the caller's number")
	next, _ := body["next_cursor"].(string)
	require.NotEmpty(t, next, "the clamped page names the cursor that reaches the rest")
}

func TestAnEmptyCollectionPageCarriesAnArrayAndACursorField(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	id, _ := seedKeyedInstance(t, h)

	for path, collection := range map[string]string{
		"/v1/instances/" + id + "/frames":                       "frames",
		"/v1/instances/" + id + "/messages":                     "messages",
		"/v1/instances/" + id + "/assets":                       "assets",
		"/v1/instances/" + id + "/breakpoints":                  "breakpoints",
		"/v1/instances/" + id + "/breakpoint-hits":              "hits",
		"/v1/admin/diagnostics/held-frames":                     "frames",
		"/v1/admin/diagnostics/parked-nodes":                    "parked_nodes",
		"/v1/admin/diagnostics/producer-outbox":                 "entries",
		"/v1/admin/diagnostics/lifecycle-outbox":                "services",
		"/v1/lineage/runs/" + uuid.NewString() + "/ancestors":   "ancestors",
		"/v1/lineage/runs/" + uuid.NewString() + "/descendants": "descendants",
		"/v1/lineage/by-producer/no-such-producer":              "records",
	} {
		status, body := h.httpJSON(t, "GET", path, nil)
		require.Equal(t, http.StatusOK, status, body)
		rows, present := body[collection]
		require.True(t, present, "GET %s names its collection %q; body=%v", path, collection, body)
		require.NotNil(t, rows, "an empty page serializes %q as [] and never as null; body=%v", collection, body)
		require.Len(t, rows, 0)
		_, hasCursor := body["next_cursor"]
		require.True(t, hasCursor, "next_cursor is present on every page; body=%v", body)
	}
}

// @concept: breakpoint
func TestBreakpointListWalksItsCursorToTheWholeSet(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	id, _ := seedKeyedInstance(t, h)
	created := map[string]bool{}
	for i := 0; i < 3; i++ {
		status, out := h.httpJSON(t, "POST", "/v1/instances/"+id+"/breakpoints", map[string]any{
			"checkpoint":  "before_dispatch",
			"matcher":     map[string]any{"node_type": "root"},
			"ttl_seconds": 600 + i,
		})
		require.Equal(t, http.StatusCreated, status, out)
		created[out["breakpoint_id"].(string)] = true
	}

	status, whole := h.httpJSON(t, "GET", "/v1/instances/"+id+"/breakpoints", nil)
	require.Equal(t, http.StatusOK, status, whole)
	var unpagedOrder []string
	var unpagedCreatedAt []string
	for _, raw := range whole["breakpoints"].([]any) {
		row := raw.(map[string]any)
		unpagedOrder = append(unpagedOrder, row["breakpoint_id"].(string))
		unpagedCreatedAt = append(unpagedCreatedAt, row["created_at"].(string))
	}
	require.Len(t, unpagedOrder, 3)
	require.IsNonDecreasing(t, unpagedCreatedAt, "the route lists breakpoints oldest first")

	seen := map[string]bool{}
	var walkOrder []string
	cursor := ""
	pages := 0
	for {
		path := fmt.Sprintf("/v1/instances/%s/breakpoints?limit=2", id)
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		status, body := h.httpJSON(t, "GET", path, nil)
		require.Equal(t, http.StatusOK, status, body)
		rows := body["breakpoints"].([]any)
		for _, raw := range rows {
			bpID := raw.(map[string]any)["breakpoint_id"].(string)
			seen[bpID] = true
			walkOrder = append(walkOrder, bpID)
		}
		pages++
		require.LessOrEqual(t, pages, 4, "the cursor walk terminates")
		next, _ := body["next_cursor"].(string)
		if next == "" || len(rows) == 0 {
			break
		}
		cursor = next
	}
	require.Equal(t, 2, pages, "the route returns three breakpoints over two pages at limit=2")
	require.Equal(t, created, seen)
	require.Equal(t, unpagedOrder, walkOrder, "paging must not reorder the collection")

	badStatus, body := h.httpJSON(t, "GET", "/v1/instances/"+id+"/breakpoints?cursor=not-a-cursor", nil)
	require.Equal(t, http.StatusBadRequest, badStatus, body)
}

func concretePathFor(pattern, instanceID, handleID string) string {
	segments := strings.Split(pattern, "/")
	for i, seg := range segments {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		switch strings.Trim(seg, "{}") {
		case "idOrKey":
			segments[i] = instanceID
		case "claim_handle_id":
			segments[i] = handleID
		case "producer_name":
			segments[i] = "some-producer"
		case "source_type":
			segments[i] = "run"
		default:
			segments[i] = uuid.NewString()
		}
	}
	return strings.Join(segments, "/")
}

// @decision: mcp-http-parity
func TestEveryPaginatingRouteDeclaresItsPaginationOnItsMCPTools(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instanceID, _ := seedKeyedInstance(t, h)
	handleID := uuid.NewString()
	schemas := builtinSchemas()

	checked := 0
	for _, action := range BuildV1Registry().AllActions() {
		entry, _ := BuildV1Registry().Entry(action)
		for _, route := range entry.Routes {
			if route.Method != http.MethodGet {
				continue
			}
			path := concretePathFor(route.Path, instanceID, handleID)
			if strings.Contains(path, "{") {
				continue
			}
			status, body := h.httpJSON(t, http.MethodGet, path, nil)
			if status != http.StatusOK {
				continue
			}
			if _, paginates := body["next_cursor"]; !paginates {
				continue
			}
			checked++
			declared := false
			for _, tool := range entry.MCPTools {
				schema := string(schemas[tool])
				if strings.Contains(schema, `"limit"`) && strings.Contains(schema, `"cursor"`) {
					declared = true
				}
			}
			require.True(t, declared,
				"GET %s answers next_cursor, but no tool of action %q (%v) declares limit and cursor; "+
					"an agent reads the schema to learn a tool's arguments, so an accepted-but-undeclared parameter is invisible",
				route.Path, action, entry.MCPTools)
		}
	}
	require.Greater(t, checked, 10, "the check reached only %d paginating routes; it is inspecting almost nothing", checked)
}
