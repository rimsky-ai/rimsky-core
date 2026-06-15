// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-rimsky-health-check end-to-end acceptance proof.
//
// Spec source-of-intent:
//
//	.ok-planner/specs/2026-06-08-design-corpus-bootstrap-design.md
//	§STORY-rimsky-health-check.
//
// Story: "As an operator running rimsky behind a load balancer or k8s
// liveness/readiness probe, I can query `GET /health` (or `rimsky
// health` CLI) and get back the control-api's deployment health
// status, so that infrastructure operators have a probe surface to
// gate traffic on."
//
// Acceptance: against a running control-api, a request to the health
// surface returns a successful response while the deployment is healthy
// and a non-success response when a critical dependency (persistence
// reachable, etc.) is down. The route requires no authentication
// (probes don't carry bearer tokens) and is fast (probe-suitable).
//
// LOAD-BEARING FALSIFIER (the property this proof must pin):
// "Health route returns success while a critical dependency is down
//
//	(false-positive), OR requires auth (incompatible with anonymous
//	probes)."
//
// Decisive RED-vs-GREEN discriminators driven through the real assembled
// product (control-api over HTTP, real persistence via testcontainers
// Postgres):
//
//  1. Healthy stack baseline — issue `GET /v1/health` with NO
//     `Authorization` header. The response is 2xx and the body decodes
//     as the documented healthResponse shape (status / supervisors /
//     node_counts). A cheaper shape that elided the body (returning a
//     bare empty 2xx) would red-flag here.
//
//  2. Anonymous-mode-closes leg (the "requires auth" falsifier). The
//     scenario harness wires `AuthState` into the control-api, and
//     minting the first admin api-key via `POST /v1/auth/keys` closes
//     anonymous mode for every other authed route. After the mint, the
//     same `GET /v1/health` request — STILL with NO bearer — must
//     succeed. The route is registered on the /v1/ sub-router BEFORE
//     the auth middleware group (controlapi/app.go) precisely so that
//     load-balancer / k8s probes can reach it without a token; a
//     cheaper-shape implementation that registers /v1/health INSIDE
//     the auth group would return 401 here once anonymous mode closes,
//     which red-flags the second falsifier leg.
//
//  3. Severed-persistence leg (the "false-positive" falsifier). Closing
//     the underlying *pgxpool.Pool (the in-process equivalent of
//     stopping the Postgres container — every persistence call against
//     a closed pool fails) and re-issuing `GET /v1/health` must return
//     a non-success status. The handler queries `Supervisors().List`
//     and `Nodes().CountByState` inside a real transaction; once the
//     pool is closed those queries error, `writeError` maps that to
//     HTTP 500, and the probe sees a non-2xx. A cheaper-shape
//     implementation that returned a canned `{status:"ok"}` without
//     touching persistence would stay 200 here and red-flag the
//     false-positive leg.
//
// @story: rimsky-health-check
package scenarios

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestHealthCheck drives STORY-rimsky-health-check's full acceptance
// through the assembled control-api: healthy baseline, anonymous-after-
// keys-exist, and severed-persistence non-success.
func TestHealthCheck(t *testing.T) {
	t.Parallel()

	// @deliberate: Disable supervisor + scheduler. The proof exercises the control-api
	// alone; the supervisor / scheduler are not load-bearing for the
	// health route and their background sweeps would log noise once the
	// pool is closed in step 3 (they target the same closed pool). Per
	// concept:module-layout, the control-api boots independently of the
	// runtime sweeps.
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
	})

	healthURL := h.ControlBase + "/v1/health"

	{
		req, err := http.NewRequest(http.MethodGet, healthURL, nil)
		require.NoError(t, err)
		// @deliberate: Explicitly: NO Authorization header — probes don't carry one.
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, "GET /v1/health: healthy baseline")
		defer resp.Body.Close()
		require.GreaterOrEqual(t, resp.StatusCode, 200, "healthy baseline: status %d not 2xx", resp.StatusCode)
		require.Less(t, resp.StatusCode, 300, "healthy baseline: status %d not 2xx", resp.StatusCode)

		// @deliberate: Body shape: the handler returns the documented healthResponse
		// (status / supervisors / node_counts). Decode liberally — we
		// don't pin every key, just that the body is a JSON object with
		// the load-bearing `status` field set.
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
		// @deliberate: Non-success = NOT 2xx. The exact code is implementation
		// detail; the load-bearing property is that the probe sees a
		// failure when persistence is unreachable.
		isSuccess := resp.StatusCode >= 200 && resp.StatusCode < 300
		require.False(t, isSuccess,
			"FALSIFIER (false-positive): /v1/health returned 2xx (%d) while persistence is unreachable; a probe relying on this would route traffic to a broken deployment",
			resp.StatusCode)
	}

	t.Logf("STORY-rimsky-health-check GREEN: healthy=2xx; anonymous-after-mint=2xx; persistence-down=non-2xx")
}
