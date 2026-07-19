// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: asset-management
// @concept: asset
// @concept: lineage-record

package asset_management

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStory_AssetManagement_ForwardLineageToConsumingRun(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint: "grpc://" + endpoint,
					Protocols: []string{
						config.ProtocolClaimProducer,
						claimproducer.ProtocolDataProcessing,
					},
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})

	h.Stub.WhenType("producer").Success(map[string]any{"ok": true}, true, "produced")
	h.Stub.WhenType("consumer").Success(map[string]any{"out": "done"}, true, "done")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "asset-management-forward-lineage", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "producer", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "content",
					Selector: "/asset-management/forward-lineage",
					Intent:   "rw",
					Alias:    "dataset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "consumer", Executor: "stub"},
				scenario.WithSubscribes(
					spec.SubscriptionEntry{Node: "producer", Type: "terminal/*", ForceUpstreamRefresh: spec.BoolPtr(false)},
					spec.SubscriptionEntry{Node: "producer", Type: "attribute/ok/changed", ForceUpstreamRefresh: spec.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"read_asset_output": map[string]any{
							"type":   "boolean",
							"source": "{{nodes.producer.attribute.ok}}",
						},
						"out": map[string]any{"type": "string", "readOnly": true},
					},
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-asset-management-forward-lineage", map[string]any{})
	producerNode := h.FindNode(iid, "producer")
	require.NotNil(t, producerNode, "producer node row must exist")
	consumerNode := h.FindNode(iid, "consumer")
	require.NotNil(t, consumerNode, "consumer node row must exist")

	h.WaitForNodeState(producerNode.ID, cascade.NodeStateFresh)
	requireDurableCommitted(t, h, producerNode.ID)
	h.WaitForNodeState(consumerNode.ID, cascade.NodeStateFresh)

	const assetAlias = "producer.dataset"
	assetsBaseURL := h.ControlBase + "/v1/instances/" + iid.String() + "/assets"
	listBody := getJSONMap(t, assetsBaseURL)
	items, _ := listBody["assets"].([]any)
	require.Lenf(t, items, 1,
		"GET /assets must list the producer's materialized asset before the forward-lineage walk is exercised; got %d", len(items))
	assetItem, _ := items[0].(map[string]any)
	require.Equal(t, assetAlias, assetItem["alias"])

	var producerRunID string
	for producerRunID == "" {
		producerRunID = mostRecentLeafRunID(t, h, iid, producerNode.ID)
		if producerRunID == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}

	var consumerRunID string
	for consumerRunID == "" {
		candidate := mostRecentLeafRunID(t, h, iid, consumerNode.ID)
		if candidate != "" && leafRunCitesSourceRun(t, h, candidate, producerRunID) {
			consumerRunID = candidate
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}

	url := h.ControlBase + "/v1/lineage/by-source/run/" + producerRunID
	out := getJSONMap(t, url)
	records, ok := out["records"].([]any)
	require.True(t, ok, "GET %s must return a records array; got %v", url, out)

	consumerSurfaced := false
	for _, r := range records {
		item, ok := r.(map[string]any)
		require.True(t, ok)
		rec, _ := item["record"].(map[string]any)
		if rec == nil {
			continue
		}
		if rec["run_id"] == consumerRunID {
			consumerSurfaced = true
			break
		}
	}
	require.Truef(t, consumerSurfaced,
		"the forward lineage walk from the asset's materializing run (producer_run_id=%s) must surface the "+
			"consumer run (consumer_run_id=%s) that read its output via GET /v1/lineage/by-source/run/{id}; "+
			"falsifier: the forward lineage walk omits a run that demonstrably read the asset's output",
		producerRunID, consumerRunID)
}

func mostRecentLeafRunID(t *testing.T, h *scenario.Harness, instanceID shared.UUID, nodeID shared.UUID) string {
	t.Helper()
	rows, err := h.Pool.Query(h.Ctx, `
		SELECT record->>'run_id' AS run_id
		  FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run'
		   AND instance_id = $1
		   AND record->>'node_id' = $2
		 ORDER BY observed_at DESC
		 LIMIT 1
	`, instanceID.String(), nodeID.String())
	if err != nil {
		t.Fatalf("mostRecentLeafRunID: query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var runID string
	if err := rows.Scan(&runID); err != nil {
		t.Fatalf("mostRecentLeafRunID: scan: %v", err)
	}
	return runID
}

func leafRunCitesSourceRun(t *testing.T, h *scenario.Harness, runID, sourceRunID string) bool {
	t.Helper()
	var recordJSON []byte
	row := h.Pool.QueryRow(h.Ctx, `
		SELECT record
		  FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run'
		   AND record->>'run_id' = $1
		 LIMIT 1
	`, runID)
	if err := row.Scan(&recordJSON); err != nil {
		return false
	}
	var rec struct {
		SubstitutionRefs []struct {
			SourceKind        string `json:"source_kind"`
			SourceVersionOrID string `json:"source_version_or_id"`
		} `json:"substitution_refs"`
	}
	if err := json.Unmarshal(recordJSON, &rec); err != nil {
		return false
	}
	for _, ref := range rec.SubstitutionRefs {
		if ref.SourceKind == "run" && ref.SourceVersionOrID == sourceRunID {
			return true
		}
	}
	return false
}
