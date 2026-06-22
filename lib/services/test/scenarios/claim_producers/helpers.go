// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package stores

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func deployTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func createInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "claim_producers", instanceKey)
	return resp.InstanceID
}

func waitForNodeTerminal(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastSummary string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				RunSummary struct {
					ActiveCount  int `json:"active_count"`
					PendingCount int `json:"pending_count"`
					FreshCount   int `json:"fresh_count"`
					FailedCount  int `json:"failed_count"`
				} `json:"run_summary"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastSummary = fmt.Sprintf("active=%d pending=%d fresh=%d failed=%d",
					resp.RunSummary.ActiveCount, resp.RunSummary.PendingCount,
					resp.RunSummary.FreshCount, resp.RunSummary.FailedCount)
				if resp.RunSummary.FailedCount > 0 {
					return
				}
				if resp.RunSummary.FreshCount > 0 && resp.RunSummary.ActiveCount == 0 && resp.RunSummary.PendingCount == 0 {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach terminal within %v; last run_summary=%s",
		nodeType, instanceID, deadline, lastSummary)
}
