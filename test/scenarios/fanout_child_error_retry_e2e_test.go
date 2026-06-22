// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestFanOutChildErrorRetryE2E(t *testing.T) {
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
						PartitionRequest: `{"partition_keys":["a"]}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindBestEffort},
					},
					MaxRetries: node.IntPtr(2),
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/flaky": {Action: "retry"},
					},
				},
				openAttrs,
				scenario.WithClaimProducers(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-fanout-child-error-retry", map[string]any{})
	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")

	var retryObs *scenarioStubObservedRetry
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		dispatches := 0
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "fan-parent" {
				dispatches++
			}
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

type scenarioStubObservedRetry struct {
	DispatchID  string
	PriorID     string
	Disposition genv1.PriorDispatchDisposition
}
