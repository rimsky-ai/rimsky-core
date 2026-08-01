// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: rimsky-health-check
package scenarios

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHealthCheck(t *testing.T) {
	t.Parallel()

	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
	})

	healthURL := h.ControlBase + "/v1/health"

	{
		req, err := http.NewRequest(http.MethodGet, healthURL, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, "GET /v1/health: healthy baseline")
		defer resp.Body.Close()
		require.GreaterOrEqual(t, resp.StatusCode, 200, "healthy baseline: status %d not 2xx", resp.StatusCode)
		require.Less(t, resp.StatusCode, 300, "healthy baseline: status %d not 2xx", resp.StatusCode)

		var body map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body),
			"healthy baseline: decode body")
		require.Contains(t, body, "status", "healthy baseline: response must carry a status field")
		require.Equal(t, "ok", body["status"], "healthy baseline: status field must be 'ok'")
	}

	_, _ = h.MintAdminKey("health-probe-anon-test")
	{
		req, err := http.NewRequest(http.MethodGet, healthURL, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, "GET /v1/health: anonymous-after-mint")
		defer resp.Body.Close()
		require.GreaterOrEqual(t, resp.StatusCode, 200,
			"FALSIFIER (requires auth): /v1/health returned %d after anonymous mode closed; the route must NOT require a bearer token",
			resp.StatusCode)
		require.Less(t, resp.StatusCode, 300,
			"FALSIFIER (requires auth): /v1/health returned %d after anonymous mode closed; the route must NOT require a bearer token",
			resp.StatusCode)
	}

	require.NoError(t, h.Driver.Close(), "close persistence driver to sever the connection")

	{
		req, err := http.NewRequest(http.MethodGet, healthURL, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, "GET /v1/health: severed-persistence (HTTP transport must still respond)")
		defer resp.Body.Close()
		isSuccess := resp.StatusCode >= 200 && resp.StatusCode < 300
		require.False(t, isSuccess,
			"FALSIFIER (false-positive): /v1/health returned 2xx (%d) while persistence is unreachable; a probe relying on this would route traffic to a broken deployment",
			resp.StatusCode)
	}

	t.Logf("STORY-rimsky-health-check GREEN: healthy=2xx; anonymous-after-mint=2xx; persistence-down=non-2xx")
}
