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

const settlingSignalTypeTerminalSuccess = "terminal/success"

func TestControlAPINodeSettlingSignalType_E2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":             "node-signal-type-e2e",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})
	instanceID := createScenarioInstance(t, ep, templateID, "ck-node-signal-type-e2e")

	nodeID := resolveWorkerNodeID(t, ep, instanceID, "worker")

	if sig, present := getNodeSettlingSignalType(t, ep, nodeID); present {
		t.Fatalf("before any settle, GET /nodes/%s returned settling_signal_type=%q, want the key ABSENT — an unsettled node must not carry a settling signal type",
			nodeID, sig)
	}

	lineageSig := waitForObservabilitySettlingSignalType(t, ep, instanceID, "worker", 90*time.Second)
	if lineageSig != settlingSignalTypeTerminalSuccess {
		t.Fatalf("observability node read reported settling_signal_type=%q after a stub Success settle, want %q — the stub returns Success, so a non-success settle is a real settle-path defect",
			lineageSig, settlingSignalTypeTerminalSuccess)
	}

	sig, present := getNodeSettlingSignalType(t, ep, nodeID)
	if !present {
		t.Fatalf("after a real Success settle, GET /nodes/%s omits settling_signal_type — a settled node MUST carry its settling signal type on the node-detail read",
			nodeID)
	}
	if sig != settlingSignalTypeTerminalSuccess {
		t.Fatalf("GET /nodes/%s settling_signal_type=%q, want %q (the canonical signal type-path of a Success settle)",
			nodeID, sig, settlingSignalTypeTerminalSuccess)
	}
	if sig != lineageSig {
		t.Fatalf("node-detail settling_signal_type=%q disagrees with the run-tree/lineage surface's %q — both project the same persisted NodeRow column and MUST report one canonical value",
			sig, lineageSig)
	}
}

func resolveWorkerNodeID(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) string {
	t.Helper()
	path := "/v1/instances/" + instanceID + "/nodes"
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, raw := ep.GetJSON(t, path, "")
		if status == http.StatusOK {
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
					return n.ID
				}
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("node %q not found via GET %s within deadline (last status %d)", nodeType, path, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
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

func waitForObservabilitySettlingSignalType(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) string {
	t.Helper()
	path := "/v1/observability/nodes/" + instanceID + "/" + nodeType
	end := time.Now().Add(deadline)
	var (
		lastState   string
		lastSig     string
		sawDispatch bool
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, path, "")
		if status == http.StatusOK {
			var resp struct {
				Node struct {
					State              string `json:"state"`
					SettlingSignalType string `json:"settling_signal_type"`
				} `json:"node"`
				Events []struct {
					Kind string `json:"kind"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				lastSig = resp.Node.SettlingSignalType
				for _, e := range resp.Events {
					if e.Kind == "work_started" {
						sawDispatch = true
						break
					}
				}
				if sawDispatch && lastSig != "" {
					return lastSig
				}
				if lastState == "failed" {
					t.Fatalf("node %q settled in %q (settling_signal_type=%q) — the stub returns Success, so a failed settle is a real settle-path defect",
						nodeType, lastState, lastSig)
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not settle with a settling_signal_type within %v; last state=%q, settling_signal_type=%q, work_started seen=%v",
		nodeType, instanceID, deadline, lastState, lastSig, sawDispatch)
	return ""
}
