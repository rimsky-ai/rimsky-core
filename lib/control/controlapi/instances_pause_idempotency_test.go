// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: instance

package controlapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstancePauseResume_RedundantTransitionsReturn409(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID := newInstanceForMessages(t, h, "pause-idem")

	status, out := h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/pause", instID), map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["paused"])

	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/pause", instID), map[string]any{})
	require.Equal(t, http.StatusConflict, status, out,
		"pausing an already-paused instance must 409, not silently succeed")

	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/resume", instID), map[string]any{})
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["resumed"])

	status, out = h.httpJSON(t, "POST", fmt.Sprintf("/v1/instances/%s/resume", instID), map[string]any{})
	require.Equal(t, http.StatusConflict, status, out,
		"resuming a non-paused instance must 409, not silently succeed")
}
