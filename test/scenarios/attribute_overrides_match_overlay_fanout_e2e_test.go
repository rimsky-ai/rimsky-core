// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestAttributeOverridesMatchOverlayFanout_ChildKeyMatcherRoutesPerChild(t *testing.T) {
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

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tag": map[string]any{"type": "string"},
			"ok":  map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-fanout-child-key", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-child",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: "best_effort"},
					},
				},
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	overrides := map[string]any{
		"by_match": []any{
			map[string]any{
				"matcher": map[string]any{"node_type": "fan-child", "child_key": "a"},
				"overlay": map[string]any{"tag": "for-a"},
			},
			map[string]any{
				"matcher": map[string]any{"node_type": "fan-child", "child_key": "b"},
				"overlay": map[string]any{"tag": "for-b"},
			},
			map[string]any{
				"matcher": map[string]any{"node_type": "fan-child", "child_key": "c"},
				"overlay": map[string]any{"tag": "for-c"},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-fanout", map[string]any{}, overrides)

	wantTags := map[string]bool{"for-a": true, "for-b": true, "for-c": true}
	converged := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		seen := map[string]int{}
		for _, o := range h.Stub.Observed() {
			if o.NodeType != "fan-child" {
				continue
			}
			tag, _ := o.Attributes["tag"].(string)
			if tag == "" {
				continue
			}
			seen[tag]++
		}
		if len(seen) == len(wantTags) {
			ok := true
			for want := range wantTags {
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
		t.Logf("rimsky_node_runs rows for this instance:")
		h.QuerySQL(`
			SELECT r.id::text, r.node_id::text, rs.parent_run_id::text, rs.partition_key,
			       r.phase::text, r.state::text, r.claimed_by
			  FROM rimsky_node_runs r
			  JOIN rimsky_nodes n      ON n.id  = r.node_id
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE n.instance_id = $1
			 ORDER BY r.enqueued_at
		`, []any{iid}, func(scan func(...any) error) error {
			var id, nid, parent, ck, phase, state, claimedBy *string
			if err := scan(&id, &nid, &parent, &ck, &phase, &state, &claimedBy); err != nil {
				return err
			}
			t.Logf("  run id=%v node=%v parent=%v partition_key=%v phase=%v state=%v claimed_by=%v",
				strDeref(id), strDeref(nid), strDeref(parent), strDeref(ck),
				strDeref(phase), strDeref(state), strDeref(claimedBy))
			return nil
		})
		t.Logf("rimsky_claim_handles rows for this instance:")
		h.QuerySQL(`
			SELECT lh.id::text, lh.parent_claim_handle_id::text, lh.state, lh.expected_children_count
			  FROM rimsky_claim_handles lh
			  JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			 WHERE n.instance_id = $1
		`, []any{iid}, func(scan func(...any) error) error {
			var id, parent, state *string
			var expected *int64
			if err := scan(&id, &parent, &state, &expected); err != nil {
				return err
			}
			t.Logf("  claim_handle id=%v parent=%v state=%v expected_children=%v",
				strDeref(id), strDeref(parent), strDeref(state), expected)
			return nil
		})
		t.Logf("rimsky_events of interest for this instance:")
		h.QuerySQL(`
			SELECT kind, payload::text
			  FROM rimsky_events
			 WHERE instance_id = $1
			   AND kind IN ('fan_out_dispatched','fanout.children_created','attributes_substituted',
			                'attribute_overrides_match_count_incremented','terminal/success',
			                'dispatch_failed')
			 ORDER BY id
		`, []any{iid}, func(scan func(...any) error) error {
			var kind, payload string
			if err := scan(&kind, &payload); err != nil {
				return err
			}
			t.Logf("  event %s %s", kind, payload)
			return nil
		})
		t.Fatalf("each child_key matcher should fire on its own child's dispatch exactly once; observed tag distribution did not converge")
	}

	require.Eventually(t, func() bool {
		var inst *persistence.InstanceRow
		err := h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.Instances().Get(ctx, iid, tx)
			inst = r
			return err
		})
		if err != nil || inst == nil {
			return false
		}
		c := inst.AttributeOverridesMatchCounts
		return len(c) == 3 && c[0] == 1 && c[1] == 1 && c[2] == 1
	}, 10*time.Second, 50*time.Millisecond,
		"AttributeOverridesMatchCounts mismatch (want [1, 1, 1])")
}

func strDeref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
