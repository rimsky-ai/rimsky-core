// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestAllInOneSQLite_DriveNodeToTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "sqlite-all-in-one",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
				},
			},
		},
	})

	instanceID := createScenarioInstance(t, ep, templateID, "ck-sqlite-all-in-one")

	waitForDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)
}

func deployScenarioTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
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
func createScenarioInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
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
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "scenario", instanceKey)
	return resp.InstanceID
}

func waitForDispatchToFresh(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	var (
		lastState   string
		sawDispatch bool
	)
	obs, ok := ep.PollNodeObservability(t, instanceID, nodeType, deadline, func(o harness.NodeObservability) bool {
		switch {
		case o.RunSummary.FailedCount > 0:
			lastState = "failed"
		case o.RunSummary.FreshCount > 0 && o.RunSummary.ActiveCount == 0 && o.RunSummary.PendingCount == 0:
			lastState = "fresh"
		default:
			lastState = "in-flight"
		}
		sawDispatch = sawDispatch || o.HasEventKind("work_started")
		return sawDispatch && (lastState == "fresh" || lastState == "failed")
	})
	if !ok {
		t.Fatalf("node %q on instance %s did not complete a real dispatch within %v; last state=%q, work_started seen=%v",
			nodeType, instanceID, deadline, lastState, sawDispatch)
	}
	if lastState != "fresh" {
		t.Fatalf("node %q dispatched but settled in %q, want fresh — the stub executor returns "+
			"Success, so a non-fresh terminal is an orchestration-loop defect; run_summary=%+v",
			nodeType, lastState, obs.RunSummary)
	}
}
