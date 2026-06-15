// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// E4 end-to-end — `candidate_handle` reaches the fan-out leaf. A
// DataProcessing fan-out leaf's `ExecuteRequest.StoreHandle` must carry
// its OWN sub-claim's `producer_candidate_handle` (the bytes the producer
// returned from `BeginCandidate` for that partition).
//
// Pins the wire-threading for proto:executor.proto::StoreHandle.candidate_handle
// (per spec .ok-planner/specs/2026-06-02-rimsky-core-remediation-design.md
// §E4): the supervisor mints a candidate per sub-claim at fan-out
// acquisition (`runtime/runner_subclaim.go::AcquireSubClaims` →
// `DataProcessing.BeginCandidate`), persists it on
// col:rimsky_claim_handles.producer_candidate_handle, and at leaf dispatch
// reads it back onto the leaf's `ExecuteRequest.StoreHandle.candidate_handle`.
//
// Reference pattern: `fanout_success_cascade_e2e_test.go` (remote
// stub-store fan-out wiring) and `child_partition_key_e2e_test.go`
// (per-child capture via `h.Stub.Observed()`). The store entry here
// declares the `data_processing` protocol so the supervisor dials the
// stub store's DataProcessing surface (`test/support/stores/stub/server`
// runs with `EnableDataProcessing: true`), which mints one candidate per
// `BeginCandidate`.
//
// RED-then-GREEN: before the leaf-dispatch threading lands, the leaf's
// `ExecuteRequest.StoreHandle` carries an empty `candidate_handle` (the
// dispatch builder never reads the sub-claim row), so the convergence
// loop below times out. With the threading in place each leaf dispatches
// with a non-empty, per-partition-unique candidate handle.
package scenarios

import (
	"strings"
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

func TestLeafCarriesCandidateHandle(t *testing.T) {
	t.Parallel()

	// @deliberate: Remote stub store. The fixture's ClaimProducer surface advertises
	// SupportsSplitScope=true and decodes {"partition_keys":[...]} into one
	// SubScopeDescriptor per key; the same fixture's DataProcessing surface
	// (EnableDataProcessing in test/support/stores/stub/testfixture) mints a
	// candidate per BeginCandidate.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"fanout-store": {
					Endpoint: "grpc://" + endpoint,
					// @deliberate: Declare data_processing so the supervisor dials the
					// store's DataProcessing surface — without it the sub-
					// claim acquisition skips BeginCandidate and no candidate
					// handle is ever minted.
					Protocols:    []string{config.ProtocolClaimProducer, claimproducer.ProtocolDataProcessing},
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	// @deliberate: Per-child stub script: Success with a no-op attributes_delta so the
	// commit gate accepts the bag. best_effort tolerates any per-child
	// outcome — this scenario asserts the dispatch-time candidate handle,
	// not aggregation policy semantics.
	h.Stub.WhenType("fan-child").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "leaf-candidate-handle", Version: "1",
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
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-leaf-candidate-handle", map[string]any{})

	// @constraint: Each of the three children dispatches under the parent's node row with
	// NodeType="fan-child"; the stub's Observed log records each dispatch's
	// per-store candidate handle. The threading claim: each leaf carries a
	// non-empty candidate handle under the `data` store alias, and the three
	// handles are distinct (one per partition — the producer must not return
	// the same handle for distinct sub-claims).
	converged := false
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		seen := map[string]bool{}
		empties := 0
		for _, o := range h.Stub.Observed() {
			if o.NodeType != "fan-child" {
				continue
			}
			ch := o.CandidateHandles["data"]
			if len(ch) == 0 {
				empties++
				continue
			}
			// @deliberate: The stub DataProcessing fixture mints a candidate handle of
			// the form "cand:<sub_claim_id>:<idempotency_key>"; assert the
			// shape so a stray byte-blob can't masquerade as a candidate.
			if !strings.HasPrefix(string(ch), "cand:") {
				continue
			}
			seen[string(ch)] = true
		}
		if empties == 0 && len(seen) == 3 {
			converged = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !converged {
		obs := h.Stub.Observed()
		t.Logf("stub observed %d dispatches:", len(obs))
		for i, o := range obs {
			t.Logf("  [%d] node_type=%s candidate_handles=%#v", i, o.NodeType, o.CandidateHandles)
		}
		t.Logf("persisted producer_candidate_handle per sub-claim row:")
		h.QuerySQL(`
			SELECT lh.id::text,
			       lh.node_run_id::text,
			       COALESCE(LENGTH(lh.producer_candidate_handle), 0) AS handle_len
			  FROM rimsky_claim_handles lh
			  JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			 WHERE n.instance_id = $1
			   AND lh.parent_claim_handle_id IS NOT NULL
			 ORDER BY lh.id
		`, []any{iid}, func(scan func(...any) error) error {
			var id, nodeRun string
			var handleLen int
			if err := scan(&id, &nodeRun, &handleLen); err != nil {
				return err
			}
			t.Logf("  sub-claim %s node_run_id=%s producer_candidate_handle_len=%d", id, nodeRun, handleLen)
			return nil
		})
		t.Fatalf("each fan-out leaf should dispatch with a non-empty, per-partition-unique candidate_handle on its StoreHandle; did not converge")
	}
}
