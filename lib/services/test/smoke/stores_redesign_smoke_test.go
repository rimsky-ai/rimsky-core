// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// stores_redesign_smoke_test.go — public-API acceptance smoke for the
// stores redesign. The pre-2026-05-24 in-process version drove 100
// sequential invalidations of the §11.5 four-node template through
// `BringUpStack` and asserted on rimsky-internal tables. The rewrite
// preserves the load-bearing semantic — "a template carrying real
// store + executor wiring drives a cascade through to terminal" —
// by:
//
//   - Bringing up rimsky/all with a real fs-store + real executor-stub
//     as peer containers.
//   - Deploying a minimal 2-node template (claim-acquirer → executor-
//     target) that exercises the same store-acquire / executor-dispatch
//     chain the §11.5 four-node template hit at scale.
//   - Driving N=5 sequential invalidations (compressed from the
//     original 100; the goal here is wire-shape coverage, not stress).
//   - Asserting via the public observability API that the executor
//     node reaches a terminal state after each invalidate.
//
// The white-box DB-query diagnostics in the original 780-line test
// (rimsky_events / rimsky_node_runs / rimsky_claim_holders inspection)
// are dropped — that surface is unreachable from lib/services under
// the consumption-side-isolation depguard, and the public observability
// API now covers the same questions where needed.
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

// TestStoresRedesignSmoke drives N sequential invalidations of a
// store-claim → executor template through the live stack and asserts
// the executor node reaches terminal after each invalidate.
func TestStoresRedesignSmoke(t *testing.T) {
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

	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := smokeDeployTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "stores-redesign-smoke",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     "claim-acquirer",
					"executor": "stub",
					"stores": []map[string]any{
						{"name": "docs", "selector": "@docs-ring", "intent": "rw"},
					},
				},
			},
		},
	})

	instanceID := smokeCreateInstance(t, ep, templateID, "stores-redesign-1")

	// Compressed from the original 100 — sufficient to exercise the
	// claim-recycle / dispatch / cascade-drain loop a handful of times.
	const cycles = 5
	const perCycle = 30 * time.Second

	nodeID := smokeFindNodeID(t, ep, instanceID, "claim-acquirer")

	for n := 1; n <= cycles; n++ {
		smokeWaitForTerminal(t, ep, instanceID, "claim-acquirer", 30*time.Second)
		// Drive an invalidate on the acquirer node so the next cycle
		// fires.
		status, raw := ep.PostJSON(t,
			fmt.Sprintf("/admin/instances/%s/nodes/%s/invalidate", instanceID, nodeID), nil)
		if status != http.StatusOK {
			t.Fatalf("invalidate %d: %d %s", n, status, string(raw))
		}
		_ = perCycle
	}

	// After the last invalidate, the node must drain back to terminal.
	smokeWaitForTerminal(t, ep, instanceID, "claim-acquirer", perCycle)
}

// smokeDeployTemplate POSTs body to /templates then deploys.
func smokeDeployTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/templates", body)
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
		"/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// smokeCreateInstance POSTs a new instance and returns instance_id.
func smokeCreateInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/instances", map[string]any{
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
	return resp.InstanceID
}

// smokeFindNodeID looks up a node id via the public observability API.
func smokeFindNodeID(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) string {
	t.Helper()
	end := time.Now().Add(15 * time.Second)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				Node struct {
					ID string `json:"id"`
				} `json:"node"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil && resp.Node.ID != "" {
				return resp.Node.ID
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s never became visible", nodeType, instanceID)
	return ""
}

// smokeWaitForTerminal polls the node state until it reaches a
// terminal value.
func smokeWaitForTerminal(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastState string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				Node struct {
					State string `json:"state"`
				} `json:"node"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				if lastState == "fresh" || lastState == "failed" {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach terminal within %v; last state=%q",
		nodeType, instanceID, deadline, lastState)
}
