// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestChildPartitionKeyBinds(t *testing.T) {
	t.Parallel()

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

	h.Stub.WhenType("fan-child").Success(map[string]any{"ok": true}, true, "ok")

	partitionAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"partition": map[string]any{
				"type":   "string",
				"source": "{{child.partition_key}}",
			},
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "child-partition-key-binds", Version: "1",
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
				partitionAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-child-partition-key", map[string]any{})

	wantPartitions := map[string]bool{"a": true, "b": true, "c": true}
	converged := false
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		seen := map[string]int{}
		for _, o := range h.Stub.Observed() {
			if o.NodeType != "fan-child" {
				continue
			}
			p, _ := o.Attributes["partition"].(string)
			if p == "" {
				continue
			}
			seen[p]++
		}
		if len(seen) == len(wantPartitions) {
			ok := true
			for want := range wantPartitions {
				if seen[want] != 1 {
					ok = false
					break
				}
			}
			if ok {
				converged = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !converged {
		obs := h.Stub.Observed()
		t.Logf("stub observed %d dispatches:", len(obs))
		for i, o := range obs {
			t.Logf("  [%d] node_type=%s attributes=%#v", i, o.NodeType, o.Attributes)
		}
		t.Logf("rimsky_events of interest for this instance:")
		h.QuerySQL(`
			SELECT kind, payload::text
			  FROM rimsky_events
			 WHERE instance_id = $1
			   AND kind IN ('fan_out_dispatched','fanout.children_created','attributes_substituted',
			                'terminal/success','dispatch_failed','template_resolution_failed')
			 ORDER BY id
		`, []any{iid}, func(scan func(...any) error) error {
			var kind, payload string
			if err := scan(&kind, &payload); err != nil {
				return err
			}
			t.Logf("  event %s %s", kind, payload)
			return nil
		})
		t.Fatalf("each fan-out child should dispatch with `partition` resolved from {{child.partition_key}} equal to its own partition key (want exactly one dispatch per key in {a,b,c}); did not converge")
	}
}
