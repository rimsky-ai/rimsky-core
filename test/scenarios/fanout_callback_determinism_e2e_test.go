// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
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

	awaited.Until(t, "single partition RunScope should be created by AcquireSubClaims", func() bool {
		var n int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_run_scopes
			 WHERE instance_id = $1 AND partition_key <> ''
		`, []any{iid}, &n)
		return n == 1
	})

	var partitionScopeID shared.UUID
	h.QueryRowSQL(`
		SELECT id FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND partition_key <> ''
		 LIMIT 1
	`, []any{iid}, &partitionScopeID)

	var nodeRunID shared.UUID
	awaited.Until(t, "partition-child dispatch row should reach state ∈ {running, held}", func() bool {
		err := h.Pool.QueryRow(h.Ctx, `
			SELECT id FROM rimsky_node_runs
			 WHERE node_id = $1 AND run_scope_id = $2
			   AND state IN ('running', 'held')
		`, parentNode.ID, partitionScopeID).Scan(&nodeRunID)
		return err == nil
	})

	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)
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
	ackBody := postCallbackBody(t, cbBase+"/v1/callback/ack-1", body)
	var firstAck callbackAckBody
	require.NoError(t, json.Unmarshal(ackBody, &firstAck))
	require.Equal(t, "accepted", firstAck.AckStatus,
		"first callback should be accepted; got %q", firstAck.AckStatus)

	awaited.Until(t, "partition-child dispatch row should leave {active, held} after first callback", func() bool {
		var phase string
		err := h.Pool.QueryRow(h.Ctx, `
			SELECT state FROM rimsky_node_runs WHERE id = $1
		`, nodeRunID).Scan(&phase)
		if err != nil {
			return false
		}
		return phase != "running" && phase != "held"
	})

	reg := h.Supervisor.CallbackRegistry()
	require.NotNil(t, reg, "supervisor.CallbackRegistry() must not be nil")
	reg.Register("ack-2", runtime.AsyncContext{
		NodeID:       parentNode.ID,
		InstanceID:   iid,
		NodeRunID:    nodeRunID,
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
	ackBody2 := postCallbackBody(t, cbBase+"/v1/callback/ack-2", body2)
	var secondAck callbackAckBody
	require.NoError(t, json.Unmarshal(ackBody2, &secondAck))
	require.Equal(t, "rejected_run_terminal", secondAck.AckStatus,
		"second callback should be rejected as rejected_run_terminal per the determinism rule; got %q",
		secondAck.AckStatus)
	require.Nil(t, secondAck.CurrentNodeRunID,
		"this fan-out child never re-dispatched after resolving, so there is no newer in-flight "+
			"canonical successor run for the node — current_dispatch_id must be omitted, not name "+
			"the already-terminal run that was just rejected")
}

type callbackAckBody struct {
	AckStatus        string  `json:"ack_status"`
	CurrentNodeRunID *string `json:"current_dispatch_id,omitempty"`
}

func postCallbackBody(t *testing.T, url string, body []byte) []byte {
	t.Helper()
	var out []byte
	awaited.Until(t, "POST "+url+" to answer 200", func() bool {
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return false
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		out = b
		return true
	})
	return out
}
