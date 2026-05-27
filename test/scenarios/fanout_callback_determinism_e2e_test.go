// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// F4 must-pass scenario — fanout_callback_determinism_e2e.
//
// End-to-end coverage of the callback determinism rule under the
// RunScope-first reshape per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Test coverage matrix / F4" + §"Callback determinism":
//
//   - A fan-out parent dispatches a partition child into a
//     fanout_partition RunScope.
//   - The partition child's executor returns AwaitAsyncCallback.
//   - First callback arrives → driveTerminal's phase-check tx accepts;
//     run transitions out of {active, held}.
//   - A SECOND callback for the SAME partition-child dispatch_id arrives
//     after the first was applied. Per the determinism rule it MUST be
//     rejected with ack_status = "rejected_run_terminal".
//
// Wire-level assertion: both callbacks return HTTP 200 (per
// code:runtime/callback.go::handleCallback's ack-but-noop discipline),
// the first carries ack_status = "accepted", the second carries
// ack_status = "rejected_run_terminal".
//
// The second callback uses a freshly-registered ack_id pointing at the
// SAME partition-child dispatch_id (necessary because the registry is
// single-shot per ack_id — the first POST Pops the original ack_id; the
// second callback could not reach driveTerminal at all without a
// separate ack_id resolving to the same dispatch).
//
// Pins the load-bearing property:
//
//   - driveTerminal's phase-check tx rejects a callback that arrives
//     when run.phase ∉ {active, held}, returning ack_status =
//     rejected_run_terminal per the @blessed-invariant: Callback
//     determinism annotation on
//     code:runtime/callback.go::driveTerminal.
//   - The rejected dispatch lives in a fanout_partition RunScope (not
//     the main scope), so the determinism rule's RunScopeID resolution
//     must correctly resolve from the partition scope's run row — a
//     broken partition-RunScope branch would prevent the second
//     callback from being correctly identified as a duplicate.
package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/control/config"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/runtime"
	stubstore "github.com/rimsky-ai/rimsky-core/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/stores/stub/testfixture"
)

func TestFanOutCallbackDeterminismE2E(t *testing.T) {
	t.Parallel()

	// Single-partition fan-out keeps the test deterministic: the
	// fan-parent emits exactly one partition child, and that child's
	// dispatch is the one subjected to the two-callback determinism
	// check. Multi-partition would have all children sharing one stub
	// script (and one ack id), which collides.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"fanout-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	// Partition-child stub script: returns AwaitAsync with ack-1. The
	// fan-parent runs the same script — there's only one partition so
	// only one dispatch fires.
	h.Stub.WhenType("fan-parent").AwaitAsyncCallback("ack-1", 5000)

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"done": map[string]any{"type": "boolean"},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fanout-callback-determinism", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["only"]}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindBestEffort},
					},
				},
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fanout-callback-determinism", map[string]any{})
	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")

	// Wait for the partition RunScope to exist (created by AcquireSubClaims).
	require.Eventually(t, func() bool {
		var n int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_run_scopes
			 WHERE instance_id = $1 AND partition_key <> ''
		`, []any{iid}, &n)
		return n == 1
	}, 30*time.Second, 100*time.Millisecond,
		"single partition RunScope should be created by AcquireSubClaims")

	// Resolve the partition RunScope id + the partition-child's
	// dispatch id (in-flight, phase ∈ {active, held}).
	var partitionScopeID shared.UUID
	h.QueryRowSQL(`
		SELECT id FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND partition_key <> ''
		 LIMIT 1
	`, []any{iid}, &partitionScopeID)

	var dispatchID shared.UUID
	require.Eventually(t, func() bool {
		err := h.Pool.QueryRow(h.Ctx, `
			SELECT id FROM rimsky_node_runs
			 WHERE node_id = $1 AND run_scope_id = $2
			   AND phase IN ('active', 'held')
		`, parentNode.ID, partitionScopeID).Scan(&dispatchID)
		return err == nil
	}, 30*time.Second, 100*time.Millisecond,
		"partition-child dispatch row should reach phase ∈ {active, held}")

	// Pin: the dispatch lives in the PARTITION scope, not the main scope.
	// If the determinism rule were silently resolving via the main scope
	// instead of the partition scope, this assertion would catch it.
	mainScopeID := h.GetMainRunScopeID(iid)
	require.NotEqual(t, mainScopeID, partitionScopeID,
		"partition RunScope id must differ from main scope id — "+
			"the determinism rule resolves RunScopeID off the run row, so the "+
			"partition branch must be exercised distinctly from the main branch")

	// FIRST CALLBACK: success body. Expect HTTP 200 + ack_status = accepted.
	cbBase := "http://" + h.Supervisor.CallbackAddr()
	body, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"done": true},
			"changed":          true,
			"change_summary":   "first",
		},
	})
	status, ackBody := postCallbackBody(t, cbBase+"/v1/callback/ack-1", body, 10*time.Second)
	require.Equal(t, http.StatusOK, status,
		"first callback should be HTTP 200; got %d body=%s", status, string(ackBody))
	var firstAck callbackAckBody
	require.NoError(t, json.Unmarshal(ackBody, &firstAck))
	require.Equal(t, "accepted", firstAck.AckStatus,
		"first callback should be accepted; got %q", firstAck.AckStatus)

	// Wait for the partition-child run row to leave {active, held} so
	// the determinism check on the second callback is meaningful.
	require.Eventually(t, func() bool {
		var phase string
		err := h.Pool.QueryRow(h.Ctx, `
			SELECT phase FROM rimsky_node_runs WHERE id = $1
		`, dispatchID).Scan(&phase)
		if err != nil {
			return false
		}
		return phase != "active" && phase != "held"
	}, 30*time.Second, 100*time.Millisecond,
		"partition-child dispatch row should leave {active, held} after first callback")

	// SECOND CALLBACK: same dispatch_id (via a freshly registered
	// ack_id). The Registry's single-shot Pop means the second
	// callback CANNOT reuse ack-1; instead we manually register a new
	// AsyncContext under ack-2 pointing at the now-terminal dispatch.
	// driveTerminal's phase-check tx finds the run in phase ∉
	// {active, held} and rejects with "rejected_run_terminal".
	reg := h.Supervisor.CallbackRegistry()
	require.NotNil(t, reg, "supervisor.CallbackRegistry() must not be nil")
	var instanceRow *persistence.InstanceRow
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.Instances().Get(ctx, iid, tx)
		instanceRow = r
		return err
	}))
	require.NotNil(t, instanceRow)
	reg.Register("ack-2", runtime.AsyncContext{
		NodeID:       parentNode.ID,
		InstanceID:   iid,
		DispatchID:   dispatchID,
		SupervisorID: "scenario-supervisor",
		NodeType:     "fan-parent",
		Executor:     "stub",
	})

	body2, _ := json.Marshal(map[string]any{
		"success": map[string]any{
			"attributes_delta": map[string]any{"done": true},
			"changed":          false,
			"change_summary":   "second",
		},
	})
	status2, ackBody2 := postCallbackBody(t, cbBase+"/v1/callback/ack-2", body2, 5*time.Second)
	require.Equal(t, http.StatusOK, status2,
		"second callback (rejected) must still be HTTP 200 per ack-but-noop; got %d body=%s",
		status2, string(ackBody2))
	var secondAck callbackAckBody
	require.NoError(t, json.Unmarshal(ackBody2, &secondAck))
	require.Equal(t, "rejected_run_terminal", secondAck.AckStatus,
		"second callback should be rejected as rejected_run_terminal per the determinism rule; got %q",
		secondAck.AckStatus)
}

// callbackAckBody mirrors the structured response body the supervisor
// writes per spec §"HTTP callback ack body: structured response". Kept
// local so the scenarios package doesn't import the runtime-internal
// struct.
type callbackAckBody struct {
	AckStatus         string  `json:"ack_status"`
	CurrentDispatchID *string `json:"current_dispatch_id,omitempty"`
}

// postCallbackBody POSTs body to url, polling briefly until the
// supervisor's registry has the ack_id registered (the supervisor's
// dispatch goroutine may race the test's callback POST). Returns the
// final HTTP status + body bytes.
func postCallbackBody(t *testing.T, url string, body []byte, timeout time.Duration) (int, []byte) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastStatus int
	var lastBody []byte
	for time.Now().Before(deadline) {
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		lastStatus = resp.StatusCode
		lastBody = b
		if resp.StatusCode == http.StatusOK {
			return resp.StatusCode, b
		}
		time.Sleep(100 * time.Millisecond)
	}
	return lastStatus, lastBody
}
