// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// backfills_test.go — F4 integration tests: create + list + show +
// cancel against the pgtest harness.

package controlapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestBackfills_CreateListShowCancel drives a backfill end-to-end.
func TestBackfills_CreateListShowCancel(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("bf-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "bf-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)

	// Create a backfill targeting `root`.
	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/backfills", instID), map[string]any{
		"target_node": "root",
		"reason":      "smoke",
		"partition_request_override": map[string]any{
			"date_range": map[string]any{"start": "2024-01-01", "end": "2024-01-02"},
		},
	})
	require.Equal(t, http.StatusCreated, status, out)
	opID, _ := out["backfill_operation_id"].(string)
	require.NotEmpty(t, opID)
	require.NotEmpty(t, out["message_id"])

	// List backfills for the instance.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/instances/%s/backfills", instID), nil)
	require.Equal(t, http.StatusOK, status, out)
	items, _ := out["backfills"].([]any)
	require.GreaterOrEqual(t, len(items), 1)
	first := items[0].(map[string]any)
	require.Equal(t, opID, first["operation_id"])
	require.Equal(t, "root", first["target_node"])
	require.Equal(t, "smoke", first["reason"])

	// Show single backfill.
	status, out = h.httpJSON(t, "GET", "/backfills/"+opID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, opID, out["operation_id"])
	require.Equal(t, "smoke", out["reason"])

	// Partitions: not yet delivered → empty list.
	status, out = h.httpJSON(t, "GET", "/backfills/"+opID+"/partitions", nil)
	require.Equal(t, http.StatusOK, status, out)
	parts, _ := out["partitions"].([]any)
	require.Equal(t, 0, len(parts))

	// Cancel.
	status, out = h.httpJSON(t, "POST", "/backfills/"+opID+"/cancel", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["cancelled"])
}

// TestBackfills_CreateMissingTargetNode rejects missing target_node.
func TestBackfills_CreateMissingTargetNode(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplBody := validTemplateBody("bf-mt-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "bf-mt-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)

	status, _ = h.httpJSON(t, "POST", fmt.Sprintf("/instances/%s/backfills", instID), map[string]any{
		"reason": "no target",
	})
	require.Equal(t, http.StatusBadRequest, status)
}
