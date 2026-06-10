// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof that `GET /nodes/{id}` surfaces a settled node's
// settling signal type, against the REAL assembled product.
//
// S-control-api-mcp-node-detail-resolution-flavor: as an operator, when I
// fetch a node's detail via `GET /nodes/{id}`, the response carries the
// node's settling signal type so my dashboard can render whether the node
// passed, committed, or settled non-propagating — without cross-referencing
// the run-tree.
//
// Unlike a handler-altitude unit test (lib/control/controlapi/nodes_test.go,
// which seeds the column directly), this drives the REAL value path: it boots
// the rimsky-all-in-one image, wires the in-tree stub executor (which returns
// Success for every dispatch), and drives a node through a REAL settle —
// scheduler enqueue → supervisor claim → executor dispatch → auto-terminal —
// so `settling_signal_type` is written by the live runtime
// (applyTerminalComplete: a successful settle persists
// settling_signal_type = "terminal/success" on the rimsky_node_runs row),
// then projected by the live control-api handler onto the `GET /nodes/{id}`
// JSON. Nothing about the value under test is hand-seeded; the column is
// populated by the real dispatch path and read by the real handler over real
// HTTP.
//
// The story's three observable claims are each asserted at the wire:
//
//	(1) Before the node settles, `GET /nodes/{id}` omits `settling_signal_type`
//	    entirely (the persisted column is NULL for an in-flight / never-run
//	    node, and omitempty drops the key) — the "absent/empty for an unsettled
//	    node" half of the contract.
//	(2) After a real Success settle, `GET /nodes/{id}` carries
//	    `settling_signal_type: "terminal/success"` — the canonical signal
//	    type-path of the node's actual settle.
//	(3) That value equals the one the run-tree/lineage surface reports for the
//	    same node: the observability node read
//	    (`GET /v1/observability/nodes/{instance}/{type}`) projects the same
//	    persisted `NodeRow.SettlingSignalType` column, so the node-detail read
//	    and the lineage/run-tree drill-down agree on one canonical value. This
//	    is the cross-check the story demands ("the same value the
//	    run-tree/lineage surface reports for that node").
//
// If the field ever regresses off `nodeResponse` (or the projection drops it),
// the equality / presence assertions fail on the observable HTTP body — a real
// completion gap, not a Docker error.
package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// settlingSignalTypeTerminalSuccess is the canonical signal type-path a
// successful settle persists (lib/runtime/runner_terminal.go
// ::applyTerminalComplete writes `settling_signal_type = "terminal/success"`).
// The stub executor returns Success for every dispatch, so a healthy settle
// of the worker node lands exactly this value on the node-detail read.
const settlingSignalTypeTerminalSuccess = "terminal/success"

// TestControlAPINodeSettlingSignalType_E2E proves that `GET /nodes/{id}`
// carries the node's settling signal type after a real settle, omits it for an
// unsettled node, and agrees with the run-tree/lineage (observability) surface
// on the canonical value — all against the live control-api over real HTTP.
func TestControlAPINodeSettlingSignalType_E2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The stub executor must be reachable on the shared network before
	// rimsky/all starts — the control-api fires a Capabilities handshake
	// against declared executors at startup. Network first, then the
	// executor peer, then rimsky on the baked SQLite default.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	// A single worker node dispatched against the success-only stub: the
	// healthy loop settles it into `fresh` with settling_signal_type =
	// "terminal/success".
	templateID := deploySQLiteTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "node-signal-type-e2e",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})
	instanceID := createSQLiteInstance(t, ep, templateID, "ck-node-signal-type-e2e")

	// Resolve the worker node's UUID via the instance node-list surface. This
	// is the same id `GET /nodes/{id}` keys on. We capture it BEFORE the node
	// settles so we can prove the unsettled-node half of the contract against
	// the node-detail read.
	nodeID := resolveWorkerNodeID(t, ep, instanceID, "worker")

	// (1) Unsettled-node half: a freshly-created node has not run, so its
	// persisted settling_signal_type column is NULL and `GET /nodes/{id}` must
	// OMIT the `settling_signal_type` key (omitempty). Read it via the
	// node-detail surface under test, immediately after create and before the
	// dispatch loop has had time to settle it.
	//
	// A `fresh` node that has never run has no settling_signal_type; the only
	// way the key could appear here is a regression that surfaces a stale or
	// default value, which this guard catches.
	if sig, present := getNodeSettlingSignalType(t, ep, nodeID); present {
		t.Fatalf("before any settle, GET /nodes/%s returned settling_signal_type=%q, want the key ABSENT — an unsettled node must not carry a settling signal type",
			nodeID, sig)
	}

	// Drive the real dispatch loop to a settle: scheduler enqueue → supervisor
	// claim → stub dispatch (Success) → auto-terminal. We wait on the
	// observability node read (which projects the same persisted NodeRow the
	// node-detail handler reads) until the node has emitted `work_started`
	// (proving a REAL dispatch, not a default `fresh`) AND carries a non-empty
	// settling_signal_type. The returned value is the run-tree/lineage
	// surface's report of the node's settling signal type — the cross-check
	// reference for the node-detail read below.
	lineageSig := waitForObservabilitySettlingSignalType(t, ep, instanceID, "worker", 90*time.Second)
	if lineageSig != settlingSignalTypeTerminalSuccess {
		t.Fatalf("observability node read reported settling_signal_type=%q after a stub Success settle, want %q — the stub returns Success, so a non-success settle is a real settle-path defect",
			lineageSig, settlingSignalTypeTerminalSuccess)
	}

	// (2) Settled-node half + (3) cross-check: `GET /nodes/{id}` over real HTTP
	// must now carry settling_signal_type, and its value must equal both the
	// canonical "terminal/success" and the value the run-tree/lineage
	// (observability) surface reports for the same node. The node-detail
	// handler projects the persisted NodeRow.SettlingSignalType, so the two
	// reads agree on one canonical value — the story's exact requirement.
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

// resolveWorkerNodeID reads `GET /instances/{id}/nodes` and returns the UUID of
// the node with the given node_type. This is the id `GET /nodes/{id}` keys on.
func resolveWorkerNodeID(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) string {
	t.Helper()
	path := "/v1/instances/" + instanceID + "/nodes"
	// Brief retry: the node rows are materialized synchronously at instance
	// create, but the GET races the create's commit on SQLite, so guard the
	// first read.
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

// getNodeSettlingSignalType reads `GET /nodes/{id}` over real HTTP and returns
// the node-detail response's settling_signal_type along with whether the key
// was present. A missing key (omitempty drop on an unsettled node) returns
// ("", false); a present key returns its value and true. This is the exact
// surface under test (controlapi.nodeResponse projected by toNodeResponse).
func getNodeSettlingSignalType(t *testing.T, ep harness.RimskyEndpoint, nodeID string) (string, bool) {
	t.Helper()
	path := "/v1/nodes/" + nodeID
	status, raw := ep.GetJSON(t, path, "")
	if status != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200\nbody: %s", path, status, string(raw))
	}
	// Decode into a generic map so an ABSENT key is distinguishable from an
	// empty-string value — the unsettled-node contract is "key absent", which
	// a typed struct with a string field would silently coerce to "".
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

// waitForObservabilitySettlingSignalType polls the observability node read
// (`GET /v1/observability/nodes/{instance}/{type}`) until the node has emitted
// a `work_started` event (proving a REAL dispatch, not a default `fresh`) AND
// its projected settling_signal_type is non-empty, then returns that value.
// This surface projects the same persisted NodeRow.SettlingSignalType column
// the node-detail handler reads, so it is the run-tree/lineage cross-check
// reference for the value the node-detail read must agree with.
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
				// A non-success settle (e.g. `failed`) stops the loop promptly
				// so the caller's equality assertion fails fast on a real
				// defect rather than timing out.
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
