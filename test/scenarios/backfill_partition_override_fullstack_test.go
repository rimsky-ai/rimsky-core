// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Full-stack backfill partition-selector override
// (spec S-lifecycle-fullstack-terminate-backfill, facet 2).
//
// An operator starts a backfill against a fan-out node carrying a
// partition_request_override, and the override binds end-to-end through
// the REAL dispatch path (scheduler + supervisor + control-api over a
// testcontainers Postgres, against a remote stub claim-producer speaking
// SplitScope over gRPC). The supervisor materializes runs against the
// OVERRIDDEN selector — exactly the two partitions the override names —
// not the template default.
//
// This is the full-stack proof the spec demands: the partition RunScopes
// are produced by the REAL backfill → message-delivery → fan-out
// SplitScope acquisition path, NOT a fake message bus. It supersedes the
// fake-altitude payload round-trip proof in
// test/scenarios/backfill/partition_selector_override_test.go, which only
// proved the override survives in the message payload and never drove the
// engine.
//
// Load-bearing property pinned here: the bytes that reach SplitScope are
// the SUBSTITUTED override (the override genuinely binds), not the
// template default. The contrast is sharp end to end — the template
// default materializes ZERO partitions (the fan-out node's first run on
// instance creation has no trigger message, so its partition_request
// resolves to the inert `"all"` fallback the stub producer rejects), while
// the backfill override materializes EXACTLY the override's two keys.
//
// Authoring-form note: the fan-out node's partition_request uses a
// quoted-string fallback (`| "all"`), NOT a composite-object fallback
// (`| {"partition_keys":[...]}`). The directive grammar
// (lib/graph/attribute/substitution.go::directivePattern) forbids `}`
// inside a `{{…}}` directive, and parseFallbackLiteral rejects composite
// `{}`/`[]` literals, so a composite-object fallback never resolves at
// runtime — it is only ever a backfill-target-validation sentinel. The
// string fallback is the form that both passes
// attributes.ReferencesTriggerMessage (so the backfill target validates)
// AND resolves cleanly when the override IS present (the override object
// is JSON-marshaled to the {"partition_keys":[…]} bytes SplitScope wants).
//
// @concept: backfill
// @concept: fan-out
package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestBackfillPartitionOverrideFullStack(t *testing.T) {
	t.Parallel()

	// Remote stub store. Its ClaimProducer surface advertises
	// SupportsSplitScope=true and decodes {"partition_keys":[...]} into
	// one SubScopeDescriptor per key — the producer that turns the
	// override into sub-claims.
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

	// Each partition child returns Success so its leaf-run settles to
	// state=fresh. best_effort tolerates any per-child outcome — this
	// scenario asserts the override binds and the partitions materialize,
	// not aggregation policy semantics.
	h.Stub.WhenType("fan-parent").Success(map[string]any{"ok": true}, true, "ok")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	// Fan-out node wired for the backfill override: its partition_request
	// pulls partition_request_override off the trigger message, with a
	// quoted-string `"all"` default (see the authoring-form note above).
	// On instance creation the node's first run has no trigger message, so
	// the default fires; `"all"` is not a {"partition_keys":[…]} request,
	// so the producer materializes no partition for the baseline run.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "backfill-override-fullstack", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "fan-parent",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            "data",
						PartitionRequest: `{{trigger.message.payload.partition_request_override | "all"}}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindBestEffort},
					},
				},
				openAttrs,
				scenario.WithStores(scenario.AliasedClaimRef("fanout-store", "data", "rw", "data")),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-backfill-override-fullstack", map[string]any{})

	parentNode := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parentNode, "fan-parent node missing")

	// Sanity baseline: the template default materializes NO partition
	// RunScope. The fan-out node's creation run has no trigger message, so
	// its partition_request resolves to the inert `"all"` fallback, which
	// is not a {"partition_keys":[…]} request the stub producer can split;
	// the fan-out acquisition therefore never produces a partition. Hold
	// the baseline at zero across a window so we know the override (below),
	// not the default, drives every partition that materializes. Allow the
	// scheduler a few ticks to attempt the creation run before sampling.
	require.Never(t, func() bool {
		var n int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_run_scopes
			 WHERE instance_id = $1 AND partition_key <> ''
		`, []any{iid}, &n)
		return n != 0
	}, 4*time.Second, 200*time.Millisecond,
		"template default must materialize no partition RunScope (the override, not the default, must drive the fan-out)")

	// Start a backfill against fan-parent with a two-key
	// partition_request_override. The override object is JSON-marshaled
	// through substituteFanOutPartitionRequest into the
	// {"partition_keys":["region-x","region-y"]} bytes SplitScope decodes.
	body, err := json.Marshal(map[string]any{
		"target_node":                "fan-parent",
		"partition_request_override": json.RawMessage(`{"partition_keys":["region-x","region-y"]}`),
		"reason":                     "scenario backfill",
	})
	require.NoError(t, err)
	resp, err := http.Post(h.ControlBase+"/v1/instances/"+iid.String()+"/backfills",
		"application/json", bytes.NewReader(body))
	require.NoError(t, err)
	backfillBody := new(bytes.Buffer)
	_, _ = backfillBody.ReadFrom(resp.Body)
	_ = resp.Body.Close()
	require.True(t, resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK,
		"backfill POST must succeed: status=%d body=%s", resp.StatusCode, backfillBody.String())

	// Through the REAL dispatch: exactly TWO new partition RunScopes keyed
	// region-x/region-y appear — the count rises to the override's 2, NOT
	// the template default's 0. The supervisor materialized runs against
	// the OVERRIDDEN selector.
	require.Eventually(t, func() bool {
		var n int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_run_scopes
			 WHERE instance_id = $1
			   AND partition_key IN ('region-x', 'region-y')
		`, []any{iid}, &n)
		return n == 2
	}, 60*time.Second, 100*time.Millisecond,
		"the backfill override must materialize exactly two partition RunScopes keyed region-x/region-y")

	// And no partition RunScope keyed by anything else exists — the
	// override fully governed the partition set (no template-default key
	// leaked through).
	var totalPartitions int
	h.QueryRowSQL(`
		SELECT COUNT(*)
		  FROM rimsky_run_scopes
		 WHERE instance_id = $1 AND partition_key <> ''
	`, []any{iid}, &totalPartitions)
	require.Equal(t, 2, totalPartitions,
		"only the override's two partitions should exist (no template-default partition leaked through)")

	// Two fan-parent child dispatches for those keys are Observed via the
	// stub — the partition children really ran through the executor.
	require.Eventually(t, func() bool {
		count := 0
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "fan-parent" {
				count++
			}
		}
		return count >= 2
	}, 60*time.Second, 100*time.Millisecond,
		"expected two fan-parent child dispatches via the override's SplitScope")

	// Both partition children reach state=fresh on their Success terminals
	// — the override-materialized runs drive to completion.
	require.Eventually(t, func() bool {
		var freshRuns int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_node_runs r
			  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			 WHERE r.state = 'fresh'
			   AND rs.instance_id = $1
			   AND rs.partition_key IN ('region-x', 'region-y')
			   AND r.node_id = $2
		`, []any{iid, parentNode.ID}, &freshRuns)
		return freshRuns >= 2
	}, 60*time.Second, 100*time.Millisecond,
		"both override partition children should reach state=fresh after Success terminal")
}
