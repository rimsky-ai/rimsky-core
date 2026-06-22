// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFanOutCallbackDeterminismE2E(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"fanout-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

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
				scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fanout-callback-determinism", map[string]any{})
	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")

	require.Eventually(t, func() bool {
		var n int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_run_scopes
			 WHERE instance_id = $1 AND partition_key <> ''
		`, []any{iid}, &n)
		return n == 1
	}, 30*time.Second, 100*time.Millisecond,
		"single partition RunScope should be created by AcquireSubClaims")

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
			   AND state IN ('running', 'held')
		`, parentNode.ID, partitionScopeID).Scan(&dispatchID)
		return err == nil
	}, 30*time.Second, 100*time.Millisecond,
		"partition-child dispatch row should reach phase ∈ {active, held}")

	mainScopeID := h.GetMainRunScopeID(iid)
	require.NotEqual(t, mainScopeID, partitionScopeID,
		"partition RunScope id must differ from main scope id — "+
			"the determinism rule resolves RunScopeID off the run row, so the "+
			"partition branch must be exercised distinctly from the main branch")

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

	require.Eventually(t, func() bool {
		var phase string
		err := h.Pool.QueryRow(h.Ctx, `
			SELECT state FROM rimsky_node_runs WHERE id = $1
		`, dispatchID).Scan(&phase)
		if err != nil {
			return false
		}
		return phase != "running" && phase != "held"
	}, 30*time.Second, 100*time.Millisecond,
		"partition-child dispatch row should leave {active, held} after first callback")

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

type callbackAckBody struct {
	AckStatus         string  `json:"ack_status"`
	CurrentDispatchID *string `json:"current_dispatch_id,omitempty"`
}

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
