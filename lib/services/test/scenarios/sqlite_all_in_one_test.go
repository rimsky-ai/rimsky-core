// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestAllInOneSQLite_DriveNodeToTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
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

	waitForDispatchToFresh(t, ep, instanceID, "worker")
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
	return createScenarioInstanceFromSource(t, ep, templateID, instanceKey, "scenario")
}

func createScenarioInstanceFromSource(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey, wakeSource string) string {
	t.Helper()
	instanceID := createScenarioInstanceNoWake(t, ep, templateID, instanceKey)
	ep.EmptyWakeAfterCreate(t, instanceID, wakeSource, instanceKey)
	return instanceID
}

func createScenarioInstanceNoWake(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":      templateID,
		"instance_key":  instanceKey,
		"params":        map[string]any{},
		"target_daemon": "scenario-default-daemon",
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
	return resp.InstanceID
}

func nodeDispatchState(o harness.NodeObservability) string {
	switch {
	case o.RunSummary.FailedCount > 0:
		return "failed"
	case o.RunSummary.FreshCount > 0 && o.RunSummary.ActiveCount == 0 && o.RunSummary.PendingCount == 0:
		return "fresh"
	default:
		return "in-flight"
	}
}

func TestNodeDispatchState(t *testing.T) {
	cases := []struct {
		name string
		obs  harness.NodeObservability
		want string
	}{
		{
			name: "fresh when idle and fresh count positive",
			obs:  harness.NodeObservability{RunSummary: harness.NodeRunSummary{FreshCount: 1}},
			want: "fresh",
		},
		{
			name: "in-flight when fresh count positive but a run is still active",
			obs:  harness.NodeObservability{RunSummary: harness.NodeRunSummary{FreshCount: 1, ActiveCount: 1}},
			want: "in-flight",
		},
		{
			name: "in-flight when fresh count positive but a run is still pending",
			obs:  harness.NodeObservability{RunSummary: harness.NodeRunSummary{FreshCount: 1, PendingCount: 1}},
			want: "in-flight",
		},
		{
			name: "failed takes priority over fresh",
			obs:  harness.NodeObservability{RunSummary: harness.NodeRunSummary{FreshCount: 1, FailedCount: 1}},
			want: "failed",
		},
		{
			name: "in-flight when nothing settled yet",
			obs:  harness.NodeObservability{},
			want: "in-flight",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeDispatchState(tc.obs); got != tc.want {
				t.Fatalf("nodeDispatchState(%+v) = %q, want %q", tc.obs.RunSummary, got, tc.want)
			}
		})
	}
}

func waitForDispatchToFresh(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) {
	t.Helper()
	var (
		lastState   string
		sawDispatch bool
	)
	obs := ep.PollNodeObservability(t, instanceID, nodeType, func(o harness.NodeObservability) bool {
		lastState = nodeDispatchState(o)
		sawDispatch = sawDispatch || o.HasEventKind("work_started")
		return sawDispatch && (lastState == "fresh" || lastState == "failed")
	})
	if lastState != "fresh" {
		t.Fatalf("node %q dispatched but settled in %q, want fresh — the stub executor returns "+
			"Success, so a non-fresh terminal is an orchestration-loop defect; run_summary=%+v",
			nodeType, lastState, obs.RunSummary)
	}
}
