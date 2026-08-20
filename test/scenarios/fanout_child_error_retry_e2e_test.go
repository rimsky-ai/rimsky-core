// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
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

	const retryCount = 2
	awaited.Until(t, fmt.Sprintf("in-place retry to produce %d fan-parent dispatches (initial + %d retries)",
		retryCount+1, retryCount), func() bool {
		dispatchCount := 0
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "fan-parent" {
				dispatchCount++
			}
		}
		return dispatchCount >= retryCount+1
	})

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
	require.Equal(t, 1, totalRuns,
		"in-place retry must reuse a single rimsky_node_runs row for the partition child")

	var retryEventCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'transient/retry/%'`,
		[]any{parentNode.ID},
		&retryEventCount,
	)
	require.GreaterOrEqual(t, retryEventCount, retryCount,
		"each retry must emit a transient/retry/<n>/<class> audit row; expected at least %d, got %d",
		retryCount, retryEventCount)
}
