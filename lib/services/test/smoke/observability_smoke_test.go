// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package smoke

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestObservabilitySmoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	cases := []struct {
		path    string
		wantKey string
	}{
		{"/v1/observability/system/summary", "node_counts"},
		{"/v1/observability/system/health", "control_api_status"},
		{"/v1/observability/templates", "templates"},
		{"/v1/observability/instances", "instances"},
		{"/v1/observability/executors", "executors"},
		{"/v1/observability/frames", "frames"},
		{"/v1/observability/node-runs", "node_runs"},
		{"/v1/observability/events", "events"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			status, raw := ep.GetJSON(t, tc.path, "")
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", status, string(raw))
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if _, ok := body[tc.wantKey]; !ok {
				t.Fatalf("response missing key %q: %+v", tc.wantKey, body)
			}
		})
	}
}
