// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins the L5 `child_key:` matcher key end-to-end against a
// real fan-out producer. The L5 seam-pinner at
// `attribute_overrides_match_overlay_l5_merge_seam_e2e_test.go` covers
// the `node_type:`, `executor:`, and empty-matcher predicates against
// a single-node template; THIS scenario exercises the matcher-overlay
// path that the seam-pinner cannot cover: per-`child_key` routing
// driven by a fan-out node whose producer's `SplitScope` emits N
// `SubScopeDescriptor`s with distinct `partition_key`s. Each child
// dispatch carries its `partition_key` as `acq.ChildKey` into
// `runtime/runner_dispatch.go::resolveAttributes` →
// `runtime/attribute_overrides.go::applyAttributeOverrides`, where the
// `child_key:` matcher branch fires per-overlay.
//
// What this pins:
//   - The fan-out dispatch path
//     (`runtime/fanout_dispatch.go::dispatchFanOutChildren`) produces
//     one child dispatch per `child_key`, and each child's
//     `ExecuteRequest.attributes` carries the post-merge overlay for
//     ITS `child_key` only.
//   - The L5 matcher predicate's `child_key:` branch matches on the
//     dispatch-time `acq.ChildKey` (NOT on any attribute value), so
//     children with `child_key="a"` see overlay `tag: "for-a"`,
//     `child_key="b"` sees `tag: "for-b"`, etc.
//   - The per-entry counter
//     (`foundation/persistence/postgres,sqlite/instances.go::IncrementAttributeOverrideMatchCounts`)
//     advances to exactly [1, 1, 1] — each matcher fires once across
//     the three child dispatches, regardless of dispatch ordering.
//
// Reference patterns mirrored:
//   - L5 merge seam (`attribute_overrides_match_overlay_l5_merge_seam_e2e_test.go`):
//     scenario shape for overlay assertions + per-entry counter
//     assertion via `h.Persist.Instances().Get(...).AttributeOverridesMatchCounts`.
//   - Sub-graph routing (`attribute_overrides_match_overlay_subgraph_e2e_test.go`):
//     waitForObservedAttrs helper for asynchronous dispatch capture.
//   - Held-claim acquirer (`held_claim_acquirer_passes_test.go`):
//     remote stub-store wiring via `stubfixture.Start` + harness
//     `Stores:` config so the supervisor can actually call SplitScope
//     over gRPC.
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

	// Start a remote stub store-service. The testfixture always
	// enables DataProcessing, and the stub's ClaimProducer surface
	// advertises SupportsSplitScope=true unconditionally — so the
	// fan-out acquisition path's call to SplitScope reaches a live
	// implementation that decodes
	// `{"partition_keys":["a","b","c"]}` into three
	// SubScopeDescriptors with PartitionKey set per key.
	//
	// No PickPolicies are configured — the fan-out claim's selector
	// ("data") falls through the no-policy branch of `Open`, which
	// returns Available=true with the selector echoed as
	// Address+Scope. That's all the fan-out path needs: an
	// acquired parent claim from which to split.
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
	// Per-child stub script: success with a no-op attributes_delta so
	// the supervisor's commit gate doesn't reject the bag. Each child
	// dispatch arrives with NodeType="fan-child" (children re-use the
	// parent's node id + node-type per the fan-out dispatch wrapper); the
	// matcher-applied `tag` field is the per-dispatch witness.
	h.Stub.WhenType("fan-child").Success(map[string]any{"ok": true}, true, "ok")

	// Open attribute schema — the matcher overlays land `tag` as a
	// top-level string. `ok` is the executor-write-back slot the stub
	// fills. The schema must be open enough that the L5 overlay's
	// `tag` value doesn't violate the post-dispatch validator.
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
						// `claim:` references the alias declared on this
						// node's stores list. The acquisition path
						// `runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared`
						// resolves the alias to the acquired
						// parent-claim entry, then calls SplitScope on
						// that producer.
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a","b","c"]}`,
						// `best_effort` tolerates any child outcome —
						// this scenario asserts dispatch-time overlay
						// routing, not aggregation semantics, so we
						// don't want a per-child commit issue to mask
						// the matcher-overlay assertions.
						ErrorPolicy: tmplspec.AggregationPolicy{Kind: "best_effort"},
					},
				},
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	// Three by_match entries, one per child_key. Each matcher fires
	// for exactly the child whose dispatch carries the matching
	// `acq.ChildKey`; the other two children must NOT see this
	// overlay's `tag`.
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

	// All three children dispatch under the parent's node row. The
	// stub's Observed log records each dispatch's NodeType +
	// Attributes; the per-child tags are the witness for the
	// `child_key:` matcher path. We don't get the ChildKey directly
	// in the Observed entry, so the assertion is "for each expected
	// tag, exactly one dispatch carried it" — equivalent to the
	// per-`child_key` routing claim.
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
		// Diagnostic dump: what does the stub see, and what does
		// the DB say about per-child runs?
		obs := h.Stub.Observed()
		t.Logf("stub observed %d dispatches:", len(obs))
		for i, o := range obs {
			t.Logf("  [%d] node_type=%s attributes=%#v", i, o.NodeType, o.Attributes)
		}
		t.Logf("rimsky_node_runs rows for this instance:")
		// Post-RunScope-first: parent_run_id + child_key moved off
		// rimsky_node_runs onto rimsky_run_scopes (parent_run_id) and
		// rimsky_run_scopes.partition_key. Project the partition_key
		// via the run's run_scope_id so the diagnostic mirrors the
		// pre-migration shape.
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

	// Counter assertion: per-entry counter advances to exactly 1 per
	// matcher entry, regardless of child dispatch ordering. The L5
	// supervisor-side increment runs inside
	// `runtime/attribute_overrides.go::incrementMatchCountersAfterMerge`
	// after each successful merge — three matched dispatches → [1,1,1].
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

// strDeref returns "<nil>" for nil string pointers; useful for
// diagnostic log lines on nullable text columns.
func strDeref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
