// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestLeafCarriesCandidateHandle(t *testing.T) {
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
					Protocols:    []string{config.ProtocolClaimProducer, claimproducer.ProtocolDataProcessing},
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

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
				scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-leaf-candidate-handle", map[string]any{})

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
		t.Fatalf("each fan-out leaf should dispatch with a non-empty, per-partition-unique candidate_handle on its ClaimProducerHandle; did not converge")
	}
}
