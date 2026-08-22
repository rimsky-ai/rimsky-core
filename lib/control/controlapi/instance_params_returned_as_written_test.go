// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: template
// @decision: secret-at-rest-posture

package controlapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestInstanceParamsComeBackAsWrittenOnEveryReadSurface(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	tplID := registerAndDeployBody(t, h, validTemplateBody("params-as-written-"+uuid.NewString()))
	params := map[string]any{
		"region": "us-east",
		"credentials": map[string]any{
			"token": "plain-token-value",
		},
	}

	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template": tplID,
		"params":   params,
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	require.NotEmpty(t, instID)

	status, out = h.httpJSON(t, "GET", "/v1/instances/"+instID, nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, params, out["params"],
		"rimsky masks nothing in instance params: the instance read returns them as written")

	status, out = h.httpJSON(t, "GET", "/v1/instances", nil)
	require.Equal(t, http.StatusOK, status, out)
	instances, _ := out["instances"].([]any)
	found := false
	for _, raw := range instances {
		item, _ := raw.(map[string]any)
		if item["id"] != instID {
			continue
		}
		found = true
		require.Equal(t, params, item["params"],
			"the instance listing returns params as written, on the same terms as the instance read")
	}
	require.True(t, found, "the listing must carry the instance just created")
}
