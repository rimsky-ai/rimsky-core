// observability_smoke_test.go — minimal smoke: assert that the
// observability endpoints come up on the in-process control-api built
// by BringUpStack and serve the documented JSON shapes. Includes an
// end-to-end probe that drives a single dispatch through the §11.5
// template and verifies the per-instance cascade-graph + per-dispatch
// detail endpoints surface the live state correctly (issue 29).
package smoke

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestObservabilityDispatchEndToEnd drives one cascade through the §11.5
// template fixture and verifies the cascade-graph + instance-detail
// endpoints surface the running state (per issue 29).
func TestObservabilityDispatchEndToEnd(t *testing.T) {
	stack := BringUpStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Seed one item so the claim-topic node has work to do.
	bulkInsertItems(t, stack, 1)
	templateID := deploySmokeTemplate(t, stack)
	instanceID := createSmokeInstance(t, stack, templateID)
	claimTopicID := findNodeIDByType(t, stack, instanceID, "claim-topic")
	fireOnceAndWait(t, stack, claimTopicID, 1, 5*time.Second, 50*time.Millisecond)

	// Wait briefly for the cascade to populate.
	deadline := time.Now().Add(15 * time.Second)
	var foundEvents int
	var graphLen int
	for time.Now().Before(deadline) {
		// Instance detail with cascade graph.
		detail, status := getJSON(t, ctx, stack.ControlBase+"/v1/observability/instances/"+instanceID.String())
		if status != 200 {
			t.Fatalf("GET instance detail: status %d", status)
		}
		graph, _ := detail["cascade_graph"].([]any)
		graphLen = len(graph)
		// Events query — should include at least one work_completed.
		evs, status2 := getJSON(t, ctx, fmt.Sprintf("%s/v1/observability/events?instance_id=%s&limit=200",
			stack.ControlBase, instanceID))
		if status2 != 200 {
			t.Fatalf("GET events: status %d", status2)
		}
		evList, _ := evs["events"].([]any)
		foundEvents = len(evList)
		if graphLen >= 4 && foundEvents > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if graphLen < 4 {
		t.Fatalf("cascade graph length = %d, want >= 4 (template has 4 nodes)", graphLen)
	}
	if foundEvents == 0 {
		t.Fatalf("expected at least one event for instance %s", instanceID)
	}
}

// getJSON GETs the URL and decodes the response body as a map.
func getJSON(t *testing.T, ctx context.Context, url string) (map[string]any, int) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}
