// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// F2 must-pass scenario — fanout_child_error_retry_e2e.
//
// End-to-end coverage of fan-out child retry semantics under the
// RunScope-first reshape per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Test coverage matrix / F2":
//
//   - Fan-out children all return Error on first dispatch.
//   - Retry policy fires for each child.
//   - Re-dispatch stays within the same partition RunScope (no scope
//     reassignment between retries).
//   - The retried dispatch's ExecuteRequest carries prior_dispatch_id +
//     prior_dispatch_disposition = PRIOR_RETRY_AFTER_ERROR per
//     `concept:run-scope` §"Recovery-aware executor protocol".
//
// Pins two load-bearing properties of the reshape:
//
//  1. Retry path threads RunScope correctly — the partition RunScope id
//     of the original child equals the partition RunScope id of the
//     retried child (verified by SQL aggregate on rimsky_node_runs).
//  2. The recovery-aware fields populate on the wire — observed via the
//     stub's ObservedRequest.PriorDispatchID +
//     PriorDispatchDisposition surfaces (per ExecuteRequest fields
//     wired up in Task 44).
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
	"github.com/rimsky-ai/rimsky-core/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
	stubstore "github.com/rimsky-ai/rimsky-core/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/stores/stub/testfixture"
)

func TestFanOutChildErrorRetryE2E(t *testing.T) {
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
	// Children always error with `flaky` class. The retry policy fires
	// 2 times (Count: 2) before the cap stops the loop. We only need to
	// observe ONE retry to pin the recovery-aware fields.
	h.Stub.WhenType("fan-parent").Error("flaky", map[string]any{"why": "nondeterministic"})

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fan-child-error-retry", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{"partition_keys":["a"]}`, // single partition simplifies assertion
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindBestEffort},
					},
					// Retry once per `flaky` error before the runtime
					// gives up. The stub keeps returning error so the
					// node eventually fails; F2 only needs the first
					// retry-after-error dispatch to assert against.
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/flaky": {Policy: []node.PolicyAction{{Action: "retry", Count: 2}}},
					},
				},
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-fanout-child-error-retry", map[string]any{})
	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")

	// Wait for at least two dispatches for the same child (original +
	// retry). The retry dispatch is the witness for the recovery-aware
	// fields.
	var retryObs *scenarioStubObservedRetry
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		dispatches := 0
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "fan-parent" {
				dispatches++
			}
			// The retry dispatch carries prior_dispatch_id populated by
			// the runtime's retry path.
			if o.PriorDispatchID != "" && o.PriorDispatchDisposition == genv1.PriorDispatchDisposition_PRIOR_RETRY_AFTER_ERROR {
				retryObs = &scenarioStubObservedRetry{
					DispatchID:  o.DispatchID,
					PriorID:     o.PriorDispatchID,
					Disposition: o.PriorDispatchDisposition,
				}
				break
			}
		}
		if retryObs != nil && dispatches >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotNil(t, retryObs,
		"a retry dispatch should carry prior_dispatch_id + PRIOR_RETRY_AFTER_ERROR")
	require.NotEqual(t, retryObs.DispatchID, retryObs.PriorID,
		"retry dispatch id must differ from prior dispatch id")

	// The retry stays within the same partition RunScope: assert by
	// counting distinct run_scope_id values across all rimsky_node_runs
	// rows for the fan-parent node. Even with retries the original +
	// successor rows live in the SAME partition RunScope.
	var distinctScopes int
	h.QueryRowSQL(`
		SELECT COUNT(DISTINCT r.run_scope_id)
		  FROM rimsky_node_runs r
		  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
		 WHERE r.node_id = $1
		   AND rs.partition_key <> ''
	`, []any{parentNode.ID}, &distinctScopes)
	require.Equal(t, 1, distinctScopes,
		"retry dispatches must stay within the same partition RunScope")

	// Multiple dispatches for the partition child via retry.
	var totalRuns int
	h.QueryRowSQL(`
		SELECT COUNT(*)
		  FROM rimsky_node_runs r
		  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
		 WHERE r.node_id = $1
		   AND rs.partition_key <> ''
	`, []any{parentNode.ID}, &totalRuns)
	require.GreaterOrEqual(t, totalRuns, 2,
		"retry should produce at least two rimsky_node_runs rows for the partition child")
}

// scenarioStubObservedRetry captures the per-retry-dispatch fields for
// the F2 retry-after-error assertion. Local helper struct so the
// scenarios package doesn't leak a runtime-internal type.
type scenarioStubObservedRetry struct {
	DispatchID  string
	PriorID     string
	Disposition genv1.PriorDispatchDisposition
}
