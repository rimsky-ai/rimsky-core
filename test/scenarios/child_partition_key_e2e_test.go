// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// E14 end-to-end — `{{child.partition_key}}` binds at fan-out leaf
// dispatch. A fan-out leaf node whose attribute schema sources a field
// from `{{child.partition_key}}` must resolve that field to the leaf's
// OWN partition key.
//
// Pins the dispatch-context binding for the substitution-layer
// `child.partition_key` source kind (per spec
// .ok-planner/specs/2026-06-02-rimsky-core-remediation-design.md §E14):
// the resolver `graph/attribute/substitution.go::resolveChildValue`
// reads `ResolveContext.ChildPartitionKey`, and the dispatch-context
// builder must set it from the acquisition's RunScope partition key so
// each fan-out child sees its own partition.
//
// Reference pattern: `fanout_success_cascade_e2e_test.go` (remote
// stub-store fan-out wiring) and
// `attribute_overrides_match_overlay_fanout_e2e_test.go` (per-child
// `Attributes` capture via `h.Stub.Observed()`).
//
// RED-then-GREEN: before the dispatch-context binding lands, the
// `{{child.partition_key}}` directive is a STRICT source that resolves
// to ErrMissingSource (no partition key bound), so the leaf's attribute
// resolution fails (`template_resolution_failed`) and no child dispatch
// ever carries the resolved `partition` field — the convergence loop
// below times out. With the binding in place each leaf dispatches with
// `partition` equal to its own partition key.
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

	// Remote stub store. The fixture's ClaimProducer surface advertises
	// SupportsSplitScope=true and decodes {"partition_keys":[...]} into
	// one SubScopeDescriptor per key — the same wiring the F1 fan-out
	// success-cascade scenario uses.
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

	// Per-child stub script: Success with a no-op attributes_delta so the
	// commit gate accepts the bag. best_effort tolerates any per-child
	// outcome — this scenario asserts the dispatch-time resolved
	// `partition` field, not aggregation policy semantics.
	h.Stub.WhenType("fan-child").Success(map[string]any{"ok": true}, true, "ok")

	// Attribute schema: `partition` is source-bound to
	// `{{child.partition_key}}`. `ok` is the executor-write-back slot the
	// stub fills. The schema is open enough that the resolved partition
	// value doesn't violate the post-dispatch validator.
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

	// Each of the three children dispatches under the parent's node row
	// with NodeType="fan-child"; the stub's Observed log records each
	// dispatch's resolved `partition` attribute. The binding claim is:
	// each partition key in {a,b,c} appears as the resolved `partition`
	// of exactly one child dispatch.
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
