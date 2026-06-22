// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestClaimProducersRedesignSmoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	netName := harness.NewNetwork(ctx, t)

	fs := harness.StartFilesystemStore(ctx, t, netName, "store-filesystem",
		harness.FilesystemStoreSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@docs-ring": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{{"docs", "alpha"}, {"docs", "beta"}, {"docs", "gamma"}},
		})

	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := smokeDeployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":             "stores-redesign-smoke",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "claim-acquirer",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "@docs-ring", "intent": "rw"},
					},
				},
			},
		},
	})

	instanceID := smokeCreateInstance(t, ep, templateID, "stores-redesign-1")

	const cycles = 5
	const perCycle = 30 * time.Second

	for n := 1; n <= cycles; n++ {
		smokeWaitForTerminal(t, ep, instanceID, "claim-acquirer", 30*time.Second)
		status, raw := ep.PostJSON(t,
			fmt.Sprintf("/v1/instances/%s/pause", instanceID), nil)
		if status != http.StatusOK {
			t.Fatalf("pause %d: %d %s", n, status, string(raw))
		}
		status, raw = ep.PostJSON(t,
			fmt.Sprintf("/v1/instances/%s/debug/override", instanceID),
			map[string]any{
				"action":    "invalidate_node",
				"node_type": "claim-acquirer",
			})
		if status != http.StatusOK {
			t.Fatalf("debug override %d: %d %s", n, status, string(raw))
		}
		status, raw = ep.PostJSON(t,
			fmt.Sprintf("/v1/instances/%s/resume", instanceID), nil)
		if status != http.StatusOK {
			t.Fatalf("resume %d: %d %s", n, status, string(raw))
		}
		_ = perCycle
	}

	smokeWaitForTerminal(t, ep, instanceID, "claim-acquirer", perCycle)
}

func smokeDeployTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
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
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func smokeCreateInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
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
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "smoke", instanceKey)
	return resp.InstanceID
}

func smokeWaitForTerminal(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
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
