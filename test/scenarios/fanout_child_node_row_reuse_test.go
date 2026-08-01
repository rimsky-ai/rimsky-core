// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFanOutChildren_ReuseParentNodeRow_UserdataVerbatim(t *testing.T) {
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

	h.Stub.WhenType("fan-child").Success(map[string]any{"ok": true}, true, "ok")

	fanoutAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"partition": map[string]any{
				"type":   "string",
				"source": "{{child.partition_key}}",
			},
			"shared_label": map[string]any{
				"type":    "string",
				"default": "parent-userdata-v1",
			},
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fanout-child-node-row-reuse", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-child",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindBestEffort},
					},
				},
				fanoutAttrs,
				scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-fanout-node-row-reuse", map[string]any{})

	fanChild := h.FindNode(iid, "fan-child")
	if fanChild == nil {
		t.Fatalf("fan-child node missing from instance")
	}

	var nodeRowCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_nodes WHERE instance_id = $1 AND node_type = 'fan-child'`,
		[]any{iid}, &nodeRowCount,
	)
	if nodeRowCount != 1 {
		t.Fatalf("fan-out must not create a separate node row per child; want exactly 1 node row for type fan-child, got %d", nodeRowCount)
	}

	h.WaitForAllRunsTerminal(fanChild.ID)

	var distinctNodeIDs int
	h.QueryRowSQL(
		`SELECT count(DISTINCT node_id) FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{fanChild.ID}, &distinctNodeIDs,
	)
	if distinctNodeIDs != 1 {
		t.Fatalf("every fan-out child run must carry the parent's node_id verbatim, got %d distinct node_ids", distinctNodeIDs)
	}

	var childRunCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND state IN ('fresh','failed')`,
		[]any{fanChild.ID}, &childRunCount,
	)
	if childRunCount < 3 {
		t.Fatalf("expected at least 3 terminal node runs (parent + 3 children folded into one node row), got %d", childRunCount)
	}

	seenPartitions := map[string]int{}
	labels := map[string]int{}
	for _, o := range h.Stub.Observed() {
		if o.NodeType != "fan-child" {
			continue
		}
		p, _ := o.Attributes["partition"].(string)
		if p == "" {
			continue
		}
		seenPartitions[p]++
		label, _ := o.Attributes["shared_label"].(string)
		labels[label]++
	}
	if len(seenPartitions) != 3 || seenPartitions["a"] != 1 || seenPartitions["b"] != 1 || seenPartitions["c"] != 1 {
		t.Fatalf("expected exactly one dispatch per partition key {a,b,c}, got %+v", seenPartitions)
	}
	if len(labels) != 1 {
		t.Fatalf("parent-declared static-default attribute must reach every fan-out child verbatim and identically; got divergent values %+v", labels)
	}
	if labels["parent-userdata-v1"] != 3 {
		t.Fatalf("expected all 3 children to observe the parent's static-default value verbatim, got %+v", labels)
	}
}
