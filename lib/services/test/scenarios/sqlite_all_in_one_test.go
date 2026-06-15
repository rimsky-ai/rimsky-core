// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof that the rimsky-all-in-one image's baked SQLite
// default actually orchestrates. Every other full-stack path in the
// repo runs on Postgres: the scenario harness (test/support/scenario)
// is Postgres-only, and the services harness (test/harness) reconfigures
// the all-in-one image to a Postgres testcontainer. SQLite is exercised
// at the persistence-conformance layer and under the control-api auth
// scenarios, but the full scheduler → supervisor → executor → terminal
// loop running on one SQLite file across three processes (single-writer,
// WAL, busy-timeout) was never driven end to end. A Postgres-green build
// can hide a SQLite-specific defect there (SQL dialect, locking, writer
// contention).
//
// This test closes that gap: it boots `rimsky-all-in-one:latest` on its
// baked SQLite config (WithSQLite — no Postgres container, no DSN
// override), wires the in-tree stub executor as a peer, registers and
// deploys a single-node executor template, creates an instance, and
// asserts the node reaches the terminal `fresh` state — proving the
// scheduler enqueued, the supervisor claimed and dispatched, the executor
// ran, and the auto-terminal transition landed, all against SQLite.
package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestAllInOneSQLite_DriveNodeToTerminal drives a real orchestration
// through the rimsky-all-in-one image running on its baked SQLite
// backend and asserts the worker node lands in `fresh`.
func TestAllInOneSQLite_DriveNodeToTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: control-api fires a Capabilities handshake against every
	// declared executor at startup, so the stub executor must already be
	// reachable on the shared network before rimsky-all-in-one boots —
	// network first, executor peer second, rimsky on the baked SQLite
	// default last.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	// @deliberate: single executor node and no stores — the stub executor
	// returns Success for every dispatch, so a healthy SQLite loop settles
	// the node into the terminal `fresh` state and any non-fresh terminal
	// is an orchestration-loop defect rather than a workload failure.
	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":             "sqlite-all-in-one",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
				},
			},
		},
	})

	instanceID := createScenarioInstance(t, ep, templateID, "ck-sqlite-all-in-one")

	// @deliberate: 90s mirrors the stores-scenario deadline; on SQLite the
	// loop is a single enqueue → claim → dispatch → settle, well inside
	// that budget. The assertion proves a REAL dispatch, not just a state
	// value: a node is created defaulting to `fresh`
	// (`code:lib/control/controlapi/instances.go`) and a successful run
	// also settles to `fresh` (running → fresh via ReasonHandlerComplete),
	// so `fresh` alone is ambiguous. The wait loop additionally requires a
	// `work_started` node event — emitted only after the supervisor
	// transitions the node to `running` on a committed claim
	// (`code:lib/runtime/runner_acquire.go`) — which is unambiguous proof
	// that the scheduler enqueued, the supervisor claimed and dispatched,
	// and the executor ran, all against SQLite.
	waitForDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)
}

// scenarioTerminalStates are the node states the wait loop treats as
// settled. A healthy stub dispatch settles to `fresh`; `failed` is
// accepted only so the loop stops promptly on a real defect instead of
// timing out — the explicit `fresh` assertion below then fails the test.
var scenarioTerminalStates = map[string]bool{
	"fresh":  true,
	"failed": true,
}

// deployScenarioTemplate POSTs body to /templates then deploys it. Returns
// the template id. Inlined rather than shared with the stores-package
// helper because that helper lives in `package stores` and is unexported.
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

// createScenarioInstance POSTs a new instance and returns its instance_id.
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
	return resp.InstanceID
}

// waitForDispatchToFresh polls the node-state observability route
// (which returns both the node row and its event log) until the node has
// (a) emitted a `work_started` event — proving the supervisor claimed and
// dispatched — and (b) settled into `fresh` — proving the executor ran and
// the terminal transition landed. A non-`fresh` settle, a missing
// `work_started`, or a timeout fails the test. Backend/topology-neutral:
// the SQLite all-in-one, single-process, and split-topology proofs all
// drive their stub dispatch to terminal through this loop.
func waitForDispatchToFresh(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastState   string
		sawDispatch bool
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				Node struct {
					State string `json:"state"`
				} `json:"node"`
				Events []struct {
					Kind string `json:"kind"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				for _, e := range resp.Events {
					if e.Kind == "work_started" {
						sawDispatch = true
						break
					}
				}
				if sawDispatch && scenarioTerminalStates[lastState] {
					if lastState != "fresh" {
						t.Fatalf("node %q dispatched but settled in %q, want fresh — the stub executor returns Success, so a non-fresh terminal is an orchestration-loop defect",
							nodeType, lastState)
					}
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not complete a real dispatch within %v; last state=%q, work_started seen=%v",
		nodeType, instanceID, deadline, lastState, sawDispatch)
}
