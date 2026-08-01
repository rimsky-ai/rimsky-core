// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func TestHealth_NodeCountsIncludeAllSevenStates(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	newInstanceForMessages(t, h, "health-counts")

	status, out := h.httpJSON(t, "GET", "/v1/health", nil)
	require.Equal(t, http.StatusOK, status, out)
	counts, ok := out["node_counts"].(map[string]any)
	require.True(t, ok, "node_counts must be present in the health response")
	for _, state := range []cascade.NodeState{
		cascade.NodeStatePending, cascade.NodeStateFresh, cascade.NodeStateStale,
		cascade.NodeStateRunning, cascade.NodeStateHeld, cascade.NodeStateParked,
		cascade.NodeStateFailed,
	} {
		_, present := counts[string(state)]
		require.True(t, present, "node_counts must report state %q, not silently drop it", state)
	}
}

func TestHealth_NodeCountsReportHeldAndParked(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID := newInstanceForMessages(t, h, "health-held-parked")
	instUUID := mustParseUUID(t, instID)
	rootNode := findNodeIDByType(t, h, instUUID, "root")
	frameID, _ := seedFrameForTest(t, ctx, h, instUUID, "test/health-held-parked")
	rootScope := rootRunScopeForFrame(t, ctx, h, frameID)
	seedRunInActiveFrame(ctx, t, h, rootNode.ID, frameID, rootScope, cascade.NodeStateHeld)

	status, out := h.httpJSON(t, "GET", "/v1/health", nil)
	require.Equal(t, http.StatusOK, status, out)
	counts, ok := out["node_counts"].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 1, counts[string(cascade.NodeStateHeld)],
		"a held node run must be counted in node_counts, not silently dropped from /health")
}
