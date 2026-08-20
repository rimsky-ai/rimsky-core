// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

const settlingSignalTypeTerminalSuccess = "terminal/success"

func TestControlAPINodeSettlingSignalType_E2E(t *testing.T) {
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
			"name":    "node-signal-type-e2e",
			"version": "1",
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})
	instanceID := createScenarioInstance(t, ep, templateID, "ck-node-signal-type-e2e")

	nodeID := resolveWorkerNodeID(t, ep, instanceID, "worker")

	sig := waitForControlSettlingSignalType(t, ep, nodeID)
	if sig != settlingSignalTypeTerminalSuccess {
		t.Fatalf("GET /nodes/%s settling_signal_type=%q after a stub Success settle, want %q — the stub returns Success, so a non-success settle is a real settle-path defect",
			nodeID, sig, settlingSignalTypeTerminalSuccess)
	}
}

func resolveWorkerNodeID(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) string {
	t.Helper()
	path := "/v1/instances/" + instanceID + "/nodes"
	var nodeID string
	awaited.Until(t, fmt.Sprintf("node %q to appear under GET %s", nodeType, path), func() bool {
		status, raw := ep.GetJSON(t, path, "")
		if status != http.StatusOK {
			return false
		}
		var resp struct {
			Nodes []struct {
				ID       string `json:"id"`
				NodeType string `json:"node_type"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode GET %s: %v\nbody: %s", path, err, string(raw))
		}
		for _, n := range resp.Nodes {
			if n.NodeType == nodeType && n.ID != "" {
				nodeID = n.ID
				return true
			}
		}
		return false
	})
	return nodeID
}

func getNodeSettlingSignalType(t *testing.T, ep harness.RimskyEndpoint, nodeID string) (string, bool) {
	t.Helper()
	path := "/v1/nodes/" + nodeID
	status, raw := ep.GetJSON(t, path, "")
	if status != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200\nbody: %s", path, status, string(raw))
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode GET %s: %v\nbody: %s", path, err, string(raw))
	}
	rawSig, present := body["settling_signal_type"]
	if !present {
		return "", false
	}
	var sig string
	if err := json.Unmarshal(rawSig, &sig); err != nil {
		t.Fatalf("decode settling_signal_type from GET %s: %v\nbody: %s", path, err, string(raw))
	}
	return sig, true
}

func waitForControlSettlingSignalType(t *testing.T, ep harness.RimskyEndpoint, nodeID string) string {
	t.Helper()
	var signal string
	awaited.Until(t, fmt.Sprintf("node %s to surface settling_signal_type via GET /v1/nodes/{id}", nodeID), func() bool {
		sig, present := getNodeSettlingSignalType(t, ep, nodeID)
		if !present {
			return false
		}
		signal = sig
		return true
	})
	return signal
}
