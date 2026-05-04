// observability_smoke_test.go — minimal smoke: assert that the
// observability endpoints come up on the in-process control-api built
// by BringUpStack and serve the documented JSON shapes. The full
// dashboard-proxy assertions described in plan task J1 are deferred to
// follow-up work (see plan notes file).
package smoke

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestObservabilitySmoke brings up the in-process stack and probes
// each top-level observability endpoint for a 200 response with the
// documented JSON envelope.
func TestObservabilitySmoke(t *testing.T) {
	stack := BringUpStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		path    string
		wantKey string
	}{
		{"/v1/observability/system/summary", "node_counts"},
		{"/v1/observability/system/health", "control_api_status"},
		{"/v1/observability/templates", "templates"},
		{"/v1/observability/instances", "instances"},
		{"/v1/observability/stores", "stores"},
		{"/v1/observability/executors", "executors"},
		{"/v1/observability/frames", "frames"},
		{"/v1/observability/dispatches", "dispatches"},
		{"/v1/observability/schedules", "schedules"},
		{"/v1/observability/events", "events"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			url := stack.ControlBase + tc.path
			req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", url, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if _, ok := body[tc.wantKey]; !ok {
				t.Fatalf("response missing key %q: %+v", tc.wantKey, body)
			}
		})
	}
}
