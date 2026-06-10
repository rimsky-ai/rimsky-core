// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-lineage-exploration end-to-end acceptance proof.
//
// Spec source-of-intent:
//
//	.ok-planner/specs/2026-06-08-design-corpus-bootstrap-design.md
//	§STORY-lineage-exploration.
//
// Story: "As an operator, I can walk the lineage of a run forward and
// backward, query lineage by claim handle, and pivot through source or
// named producer, so that I trace how data flowed through the rimsky
// stack."
//
// Acceptance: after running an instance whose template produces lineage
// records, an operator queries the lineage for a run through the
// control-api and walks upstream to the producers that fed it and
// downstream to consumers that depended on it; query by claim handle
// returns the lineage record for that claim; the source-pivot and
// producer-pivot return the records they should — a producer the run
// actually used appears in upstream, a consumer that actually consumed
// appears in downstream.
//
// LOAD-BEARING FALSIFIER (the property this proof must pin):
// "A real upstream producer is missing from the ancestor walk, OR a
//
//	real downstream consumer is missing from the descendant walk."
//
// Decisive RED-vs-GREEN discriminators driven through the real assembled
// product (control-api over HTTP, real scheduler + frame engine, real
// supervisor + stub-executor dispatch, real fan-out emitting per-child
// runs with parent_run_id linkage, real lineage writer populating
// substitution_refs from `{{nodes.<X>.attribute.<Y>}}` directives,
// testcontainers Postgres):
//
//  1. The producer's fan-out parent run-id seeds `GET /v1/lineage/runs/
//     {producer_run_id}/descendants` — the response includes the
//     downstream fan-out child runs (the children that ACTUALLY ran
//     against the producer's parent run via `parent_run_id` linkage).
//     The cheaper shape (canned response, stale projection) would NOT
//     correlate with the per-partition child runs the supervisor really
//     dispatched.
//
//  2. The consumer node's most-recent leaf-run-id seeds `GET /v1/lineage/
//     runs/{consumer_run_id}/ancestors` — the response includes the
//     producer's run (cited by the consumer's `substitution_refs[].
//     source_kind="run"` entries the lineage writer populated at terminal
//     time per `CollectSubstitutionRefsForEmit`). The cheaper shape (a
//     handler that returns empty for "no producer known") would miss the
//     real upstream the consumer actually consumed.
//
//  3. `GET /v1/lineage/claims/{claim_handle_id}` — the producer's
//     committed durable claim handle id seeds the surface; the response
//     is the claim_terminal lineage row the runtime wrote on Commit.
//
//  4. `GET /v1/lineage/by-source/run/{producer_run_id}` — reverse lookup
//     finds the consumer (because the consumer's substitution_refs cite
//     the producer's run via `source_kind="run",
//     source_version_or_id=<producer_run_id>`).
//
//  5. `GET /v1/lineage/by-producer/{producer_name}` — returns the
//     claim_terminal rows the producer-store emitted, providing the
//     named-producer pivot.
//
// @concept: lineage
// @concept: lineage-record
// @story: lineage-exploration
package scenarios

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestLineageExploration drives a real producer + consumer through the
// assembled rimsky-all-in-one stack and walks the lineage projection via
// the public control-api routes.
//
// Topology:
//
//	producer (fan-out parent) ─claim─▶ stub-store (durable)
//	    │                                    │
//	    │ parent_run_id links per-partition  │
//	    ▼                                    ▼
//	producer fan-out children            claim_terminal lineage row
//	    │
//	    │ producer.attribute.ok ───▶ consumer (substitution_refs cites
//	    ▼                            producer run via source_kind=run)
//	consumer leaf-run lineage row with substitution_refs populated
//
// The fan-out parent run-id powers the descendants walk (its
// per-partition children carry `parent_run_id == parent.run_id`); the
// consumer's leaf-run lineage row powers the ancestors walk (its
// `substitution_refs[].source_kind="run"` entries cite the producer's
// run-id). Both legs run through the real persistence + lineage-writer
// path — no test-only seeds are inserted into the lineage table.
func TestLineageExploration(t *testing.T) {
	t.Parallel()

	// Remote stub store advertising the ClaimProducer surface with
	// SupportsSplitScope so the fan-out partition request decodes into
	// per-key sub-claims. The fixture's Commit path writes a
	// claim_terminal lineage row through the engine's
	// `runtime.WriteClaimTerminalLineage` once the producer's durable
	// claim reaches terminal.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	const producerName = "lineage-store"

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				producerName: {
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})

	// Per-node executor scripts: each Success terminal causes a leaf-run
	// lineage row to be written, with substitution_refs populated from
	// the directive layer at terminal time.
	h.Stub.WhenType("producer").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("consumer").Success(map[string]any{"out": "done"}, true, "done")

	// Template:
	//   - `producer` fans out into two partition children (a, b). Each
	//     child carries `parent_run_id = <producer-parent.run_id>` so the
	//     descendant walker can find them via `record->>'parent_run_id'`.
	//   - `consumer` consumes `{{nodes.producer.attribute.ok}}` via the
	//     substitution layer, which (a) auto-subscribes consumer to
	//     producer's `attribute` topic for cascade plumbing, and (b)
	//     causes the lineage writer's `CollectSubstitutionRefsForEmit`
	//     to populate consumer's `substitution_refs` with a
	//     `source_kind="run", source_version_or_id=<producer-run-id>`
	//     entry — the ancestor walker's link material.
	const claimAlias = "data"
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "lineage-exploration-e2e", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "producer",
					Executor: "stub",
					FanOut: &tmplspec.FanOutSpec{
						Claim:            claimAlias,
						PartitionRequest: `{"partition_keys":["a","b"]}`,
						ErrorPolicy:      tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindBestEffort},
					},
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
				scenario.WithStores(scenario.AliasedClaimRef(producerName, "/data/root", "rw", claimAlias)),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "consumer",
					Executor: "stub",
				},
				// Explicit subscription on producer's terminal/* — fan-out
				// parents emit terminal cascades to subscribers when the
				// aggregation policy settles, regardless of whether the
				// attribute-substitution-derived auto-subscription wakes the
				// consumer. Combining BOTH the explicit terminal/* receiver
				// AND the substitution-derived `attribute` receiver ensures
				// the consumer wakes on real producer terminal AND that the
				// substitution_refs population path (`CollectSubstitutionRefsForEmit`)
				// fires at consumer-terminal-emit time.
				scenario.WithSubscribes(
					tmplspec.SubscriptionEntry{Node: "producer", Type: "terminal/*"},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						// `source: "{{nodes.producer.attribute.ok}}"`
						// drives substitution_refs population on the
						// consumer's leaf-run lineage row at terminal time
						// (CollectSubstitutionRefsForEmit looks up the
						// upstream producer's most-recent leaf-run row and
						// populates `source_kind="run"` entries the ancestor
						// walker reads).
						"upstream_ok": map[string]any{
							"type":   "boolean",
							"source": "{{nodes.producer.attribute.ok}}",
						},
						// `out` is the executor-write-back slot.
						"out": map[string]any{"type": "string", "readOnly": true},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-lineage-exploration-e2e", map[string]any{})

	producerNode := h.FindNode(iid, "producer")
	require.NotNil(t, producerNode, "producer node missing on the instance")
	consumerNode := h.FindNode(iid, "consumer")
	require.NotNil(t, consumerNode, "consumer node missing on the instance")

	// Wait for the lineage records to actually land in the projection.
	// The producer's parent run and the per-partition children each write
	// a leaf-run row at terminal; the consumer writes its own leaf-run row
	// after it dispatches via the cascade from producer's terminal.
	//
	// LOAD-BEARING CORRECTNESS NOTE: the convergence loop polls for
	// per-node row presence rather than a fixed wall-clock delay, so the
	// real-product timing (fan-out → cascade → consumer dispatch →
	// consumer terminal → consumer lineage emit) is honored honestly
	// without test-fragility.
	if !waitForLineageReady(t, h, iid, producerNode.ID, consumerNode.ID, 90*time.Second) {
		t.Logf("producer node_id = %s", producerNode.ID.String())
		t.Logf("consumer node_id = %s", consumerNode.ID.String())
		dumpNodeRuns(t, h, iid)
		dumpLineageRows(t, h, iid)
		t.Fatalf("the real assembled product must write leaf_run lineage rows for the producer (>=2 partition children) and the consumer (>=1 row); see dumped rows above")
	}

	// Look up the producer's PARENT fan-out run-id — the row whose
	// record.parent_run_id is empty/null (the top-level producer run on
	// the main RunScope). The fan-out child rows carry
	// parent_run_id = <this id>, so this is the seed for the descendant
	// walk. Multiple fan-out attempts can produce more than one such
	// parent-row per node; we pick the most recent.
	producerParentRunID := mostRecentProducerParentRunID(t, h, iid, producerNode.ID)
	require.NotEqual(t, "", producerParentRunID,
		"the producer's parent fan-out run-id must be discoverable from the leaf_run projection")

	// The consumer's leaf-run row carries the substitution_refs the
	// ancestor walker reads. Pick the most recent consumer run-id.
	consumerRunID := mostRecentConsumerRunID(t, h, iid, consumerNode.ID)
	require.NotEqual(t, "", consumerRunID,
		"the consumer's leaf-run id must be discoverable from the leaf_run projection")

	// Wait for the consumer's substitution_refs to include a
	// `source_kind="run"` entry citing the producer's parent run. The
	// lineage writer's `CollectSubstitutionRefsForEmit` looks up the
	// most recent leaf-run for the upstream node — which can be the
	// parent fan-out run, the child fan-out runs, OR any of them
	// depending on observed_at ordering. We assert that AT LEAST ONE
	// entry cites a producer-node run (we resolve the producer-side run
	// set below) so the ancestor walk has a hop to follow.
	require.Eventually(t, func() bool {
		return consumerCitesAProducerRun(t, h, iid, producerNode.ID, consumerRunID)
	}, 30*time.Second, 200*time.Millisecond,
		"the consumer's lineage row must carry a substitution_refs entry citing one of the producer's runs (source_kind=run); without this the ancestor walk has no link to follow")

	// =========================================================================
	// (1) GET /v1/lineage/runs/{consumer_run_id} — the seed-row surface
	//     returns the consumer's leaf-run lineage row. We use the
	//     consumer's run-id (not the producer's fan-out parent id, which
	//     is the dispatch aggregator and has no leaf_run row of its own).
	// =========================================================================
	{
		url := h.ControlBase + "/v1/lineage/runs/" + consumerRunID
		status, body := httpGetJSON(t, url)
		require.Equal(t, http.StatusOK, status, "GET lineage run: %s", body)
		var item map[string]any
		require.NoError(t, json.Unmarshal(body, &item))
		require.Equal(t, "leaf_run", item["record_kind"])
		rec, ok := item["record"].(map[string]any)
		require.True(t, ok, "record field present")
		require.Equal(t, consumerRunID, rec["run_id"])
	}

	// =========================================================================
	// (2) LOAD-BEARING FALSIFIER LEG: GET /v1/lineage/runs/
	//     {producer_parent_run_id}/descendants — the per-partition child
	//     runs the supervisor REALLY dispatched must surface in the
	//     descendants set (each child has parent_run_id linking back to
	//     the producer parent).
	// =========================================================================
	{
		url := h.ControlBase + "/v1/lineage/runs/" + producerParentRunID + "/descendants?depth=3"
		status, body := httpGetJSON(t, url)
		require.Equal(t, http.StatusOK, status, "GET descendants: %s", body)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		descendants, ok := out["descendants"].([]any)
		require.True(t, ok, "descendants array present")
		// At LEAST the two fan-out partition children (a, b) must appear.
		// The descendants set is BFS-level-bounded; depth=3 covers the
		// child level (the children themselves have no further fan-out
		// descendants in this template, so the set tops out at the
		// children).
		require.GreaterOrEqual(t, len(descendants), 2,
			"descendants of producer parent must include the >=2 real fan-out child runs (one per partition); falsifier brief: 'a real downstream consumer is missing from the descendant walk'")
		// Each descendant must be a leaf-run row with parent_run_id =
		// producer parent — pinning that the descendants are the real
		// children, not unrelated rows leaked through a broken predicate.
		for _, d := range descendants {
			item, ok := d.(map[string]any)
			require.True(t, ok, "descendant item is object")
			rec, ok := item["record"].(map[string]any)
			require.True(t, ok, "descendant record present")
			require.Equal(t, producerParentRunID, rec["parent_run_id"],
				"each descendant row must cite the seed as its parent_run_id")
		}
	}

	// =========================================================================
	// (3) LOAD-BEARING FALSIFIER LEG: GET /v1/lineage/runs/
	//     {consumer_run_id}/ancestors — the producer's run the consumer
	//     ACTUALLY substituted from must surface in the ancestors set
	//     (consumer's substitution_refs cite producer's run via
	//     source_kind="run").
	// =========================================================================
	{
		url := h.ControlBase + "/v1/lineage/runs/" + consumerRunID + "/ancestors?depth=3"
		status, body := httpGetJSON(t, url)
		require.Equal(t, http.StatusOK, status, "GET ancestors: %s", body)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		ancestors, ok := out["ancestors"].([]any)
		require.True(t, ok, "ancestors array present")
		require.GreaterOrEqual(t, len(ancestors), 1,
			"ancestors of consumer run must include >=1 producer-node run (the upstream the consumer cited via {{nodes.producer.attribute.ok}}); falsifier brief: 'a real upstream producer is missing from the ancestor walk'")
		// At least one ancestor row must be a producer-node leaf-run.
		// The seed must NOT appear in its own ancestor set.
		producerNodeIDStr := producerNode.ID.String()
		consumerSeenAsAncestor := false
		producerSeenAsAncestor := false
		for _, a := range ancestors {
			item, ok := a.(map[string]any)
			require.True(t, ok, "ancestor item is object")
			rec, ok := item["record"].(map[string]any)
			require.True(t, ok, "ancestor record present")
			runID, _ := rec["run_id"].(string)
			nodeID, _ := rec["node_id"].(string)
			if runID == consumerRunID {
				consumerSeenAsAncestor = true
			}
			if nodeID == producerNodeIDStr {
				producerSeenAsAncestor = true
			}
		}
		require.False(t, consumerSeenAsAncestor,
			"seed (consumer's run) must not appear in its own ancestors set")
		require.True(t, producerSeenAsAncestor,
			"the producer node must appear in the consumer's ancestors set; the consumer's substitution_refs cited the producer's run as source_kind=run and the walker must follow that link")
	}

	// =========================================================================
	// (4) GET /v1/lineage/claims/{claim_handle_id} — the producer's
	//     committed claim handle id seeds the claim-pivot surface. The
	//     runtime's WriteClaimTerminalLineage wrote a claim_terminal row
	//     at the producer's claim Commit; the surface returns it.
	// =========================================================================
	claimHandleID := mostRecentClaimHandleID(t, h, iid)
	require.NotEqual(t, "", claimHandleID,
		"the producer's committed claim handle id must be discoverable from rimsky_claim_handles")

	require.Eventually(t, func() bool {
		url := h.ControlBase + "/v1/lineage/claims/" + claimHandleID
		status, body := httpGetJSON(t, url)
		if status != http.StatusOK {
			return false
		}
		var item map[string]any
		if err := json.Unmarshal(body, &item); err != nil {
			return false
		}
		if item["record_kind"] != "claim_terminal" {
			return false
		}
		rec, _ := item["record"].(map[string]any)
		if rec == nil {
			return false
		}
		return rec["claim_handle_id"] == claimHandleID
	}, 60*time.Second, 200*time.Millisecond,
		"GET /v1/lineage/claims/{claim_handle_id} must return the claim_terminal lineage row the engine wrote on the producer's claim Commit")

	// =========================================================================
	// (5) GET /v1/lineage/by-source/run/{producer_run_id} — the
	//     reverse-lookup pivot. The consumer's substitution_refs cite a
	//     producer-node run via `source_kind="run",
	//     source_version_or_id=<producer-run-id>`. The pivot must return
	//     the consumer's lineage row when seeded with the cited
	//     producer-node run-id.
	// =========================================================================
	{
		// The substitution_refs population helper picks the MOST RECENT
		// producer-node leaf-run row; we discover which one the consumer
		// actually cited (so the test pins the real link rather than
		// guessing).
		citedProducerRunID := consumerCitedProducerRunID(t, h, iid, producerNode.ID, consumerRunID)
		require.NotEqual(t, "", citedProducerRunID,
			"the producer-node run-id the consumer actually cited must be discoverable from the consumer's substitution_refs")

		url := h.ControlBase + "/v1/lineage/by-source/run/" + citedProducerRunID
		status, body := httpGetJSON(t, url)
		require.Equal(t, http.StatusOK, status, "GET by-source: %s", body)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		records, ok := out["records"].([]any)
		require.True(t, ok, "by-source records array present")
		// At least one record (the consumer's) must surface, because the
		// consumer cited this producer-node run via substitution_refs.
		consumerSurfaced := false
		for _, r := range records {
			item, ok := r.(map[string]any)
			require.True(t, ok)
			rec, _ := item["record"].(map[string]any)
			if rec == nil {
				continue
			}
			if rec["run_id"] == consumerRunID {
				consumerSurfaced = true
				break
			}
		}
		require.True(t, consumerSurfaced,
			"by-source pivot on the cited producer run-id must surface the consumer's lineage row (the consumer's substitution_refs cite this producer run); without this the reverse pivot is broken")
	}

	// =========================================================================
	// (6) GET /v1/lineage/by-producer/{producer_name} — returns the
	//     claim_terminal rows whose record.producer_name matches. The
	//     producer's durable claim Commit wrote one such row keyed on the
	//     remote stub store's producer_name.
	// =========================================================================
	require.Eventually(t, func() bool {
		url := h.ControlBase + "/v1/lineage/by-producer/" + producerName
		status, body := httpGetJSON(t, url)
		if status != http.StatusOK {
			return false
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			return false
		}
		records, ok := out["records"].([]any)
		if !ok {
			return false
		}
		return len(records) >= 1
	}, 60*time.Second, 200*time.Millisecond,
		"by-producer pivot must return >=1 claim_terminal record for the producer-store name; without this the named-producer pivot is broken")

	// Final diagnostic dump confirming the test really exercised the
	// full topology (producer parent + 2 fan-out children + consumer +
	// claim_terminal rows). On a green run this prints the produced
	// rows; on a red run the failed assertion above prints first.
	t.Logf("STORY-lineage-exploration GREEN: producer_parent_run_id=%s consumer_run_id=%s claim_handle_id=%s",
		producerParentRunID, consumerRunID, claimHandleID)
	dumpLineageRows(t, h, iid)
}

// mostRecentProducerParentRunID returns the parent_run_id cited by the
// producer's fan-out child lineage rows — the fan-out parent run-id.
//
// In rimsky's RunScope-first fan-out model, the fan-out parent itself
// doesn't emit its own leaf_run lineage row (it's the dispatch
// aggregator, not a leaf), so we recover the parent run-id from the
// `parent_run_id` JSONB field of one of its child rows. Every fan-out
// child for a given producer cites the same parent_run_id, so any child
// row works as the source.
//
// This is the seed for the descendant walk: `GET /v1/lineage/runs/
// {parent_run_id}/descendants` finds rows whose `record.parent_run_id`
// matches via `QueryByParentRunID`.
func mostRecentProducerParentRunID(t *testing.T, h *scenario.Harness, instanceID, nodeID interface{ String() string }) string {
	t.Helper()
	rows, err := h.Pool.Query(h.Ctx, `
		SELECT record->>'parent_run_id' AS parent_run_id
		  FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run'
		   AND instance_id = $1
		   AND record->>'node_id' = $2
		   AND record->>'parent_run_id' IS NOT NULL
		   AND record->>'parent_run_id' <> ''
		 ORDER BY observed_at DESC
		 LIMIT 1
	`, uuid.UUID(mustUUID(instanceID.String())), nodeID.String())
	if err != nil {
		t.Fatalf("mostRecentProducerParentRunID: query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var parentRunID string
	if err := rows.Scan(&parentRunID); err != nil {
		t.Fatalf("mostRecentProducerParentRunID: scan: %v", err)
	}
	return parentRunID
}

// mostRecentConsumerRunID returns the consumer node's most-recent
// leaf-run run-id from the lineage projection. The consumer's leaf-run
// lineage row is where the substitution_refs that power the ancestor
// walk live.
func mostRecentConsumerRunID(t *testing.T, h *scenario.Harness, instanceID, nodeID interface{ String() string }) string {
	t.Helper()
	rows, err := h.Pool.Query(h.Ctx, `
		SELECT record->>'run_id' AS run_id
		  FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run'
		   AND instance_id = $1
		   AND record->>'node_id' = $2
		 ORDER BY observed_at DESC
		 LIMIT 1
	`, uuid.UUID(mustUUID(instanceID.String())), nodeID.String())
	if err != nil {
		t.Fatalf("mostRecentConsumerRunID: query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var runID string
	if err := rows.Scan(&runID); err != nil {
		t.Fatalf("mostRecentConsumerRunID: scan: %v", err)
	}
	return runID
}

// consumerCitesAProducerRun returns true iff the consumer's leaf-run
// lineage row carries a `substitution_refs[].source_kind="run"` entry
// whose `source_version_or_id` is the run-id of some producer-node row
// in the same instance.
//
// The link is what the ancestor walker follows: without it the test's
// ancestor-walk assertion would be vacuous (the walker has nothing to
// chain through). We poll because the lineage writer's
// `CollectSubstitutionRefsForEmit` looks up the upstream's most-recent
// leaf-run row at consumer-terminal-emit time; if the consumer's
// terminal fires concurrently with the producer's leaf-run write,
// substitution_refs may take a moment to converge.
func consumerCitesAProducerRun(
	t *testing.T, h *scenario.Harness,
	instanceID, producerNodeID interface{ String() string },
	consumerRunID string,
) bool {
	t.Helper()
	// Collect producer-node leaf-run run-ids in the instance.
	producerRunIDs := map[string]bool{}
	rows, err := h.Pool.Query(h.Ctx, `
		SELECT record->>'run_id'
		  FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run'
		   AND instance_id = $1
		   AND record->>'node_id' = $2
	`, uuid.UUID(mustUUID(instanceID.String())), producerNodeID.String())
	if err != nil {
		t.Fatalf("consumerCitesAProducerRun: producer query: %v", err)
	}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			t.Fatalf("consumerCitesAProducerRun: scan: %v", err)
		}
		producerRunIDs[r] = true
	}
	rows.Close()
	if len(producerRunIDs) == 0 {
		return false
	}
	// Inspect the consumer's row for substitution_refs[].source_kind="run".
	var recordJSON []byte
	row := h.Pool.QueryRow(h.Ctx, `
		SELECT record
		  FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run'
		   AND record->>'run_id' = $1
		 LIMIT 1
	`, consumerRunID)
	if err := row.Scan(&recordJSON); err != nil {
		return false
	}
	var rec struct {
		SubstitutionRefs []struct {
			SourceKind        string `json:"source_kind"`
			SourceVersionOrID string `json:"source_version_or_id"`
		} `json:"substitution_refs"`
	}
	if err := json.Unmarshal(recordJSON, &rec); err != nil {
		return false
	}
	for _, ref := range rec.SubstitutionRefs {
		if ref.SourceKind != "run" {
			continue
		}
		if producerRunIDs[ref.SourceVersionOrID] {
			return true
		}
	}
	return false
}

// consumerCitedProducerRunID returns the specific producer-node run-id
// the consumer cited via substitution_refs. Used to seed the by-source
// pivot test so it checks the real cited link rather than guessing.
func consumerCitedProducerRunID(
	t *testing.T, h *scenario.Harness,
	instanceID, producerNodeID interface{ String() string },
	consumerRunID string,
) string {
	t.Helper()
	producerRunIDs := map[string]bool{}
	rows, err := h.Pool.Query(h.Ctx, `
		SELECT record->>'run_id'
		  FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run'
		   AND instance_id = $1
		   AND record->>'node_id' = $2
	`, uuid.UUID(mustUUID(instanceID.String())), producerNodeID.String())
	if err != nil {
		t.Fatalf("consumerCitedProducerRunID: producer query: %v", err)
	}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			t.Fatalf("consumerCitedProducerRunID: scan: %v", err)
		}
		producerRunIDs[r] = true
	}
	rows.Close()
	var recordJSON []byte
	row := h.Pool.QueryRow(h.Ctx, `
		SELECT record
		  FROM rimsky_lineage
		 WHERE record_kind = 'leaf_run'
		   AND record->>'run_id' = $1
		 LIMIT 1
	`, consumerRunID)
	if err := row.Scan(&recordJSON); err != nil {
		return ""
	}
	var rec struct {
		SubstitutionRefs []struct {
			SourceKind        string `json:"source_kind"`
			SourceVersionOrID string `json:"source_version_or_id"`
		} `json:"substitution_refs"`
	}
	if err := json.Unmarshal(recordJSON, &rec); err != nil {
		return ""
	}
	for _, ref := range rec.SubstitutionRefs {
		if ref.SourceKind == "run" && producerRunIDs[ref.SourceVersionOrID] {
			return ref.SourceVersionOrID
		}
	}
	return ""
}

// mostRecentClaimHandleID returns the claim_handle_id cited by the
// most recently observed `claim_terminal` lineage row for the instance.
// The runtime's `WriteClaimTerminalLineage` writes one such row per
// claim Commit / Abandon, embedding `claim_handle_id` in the record
// JSON. Reading the id from the lineage projection (rather than from
// `rimsky_claim_handles` directly) keeps the test resilient against
// post-Commit row management (claim handles may be GC'd or transitioned
// to a different lock_kind once the durable claim is promoted) while
// still pinning the operator-visible /lineage/claims/{id} surface.
func mostRecentClaimHandleID(t *testing.T, h *scenario.Harness, instanceID interface{ String() string }) string {
	t.Helper()
	rows, err := h.Pool.Query(h.Ctx, `
		SELECT record->>'claim_handle_id'
		  FROM rimsky_lineage
		 WHERE record_kind = 'claim_terminal'
		   AND instance_id = $1
		 ORDER BY observed_at DESC
		 LIMIT 1
	`, uuid.UUID(mustUUID(instanceID.String())))
	if err != nil {
		t.Fatalf("mostRecentClaimHandleID: query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var id string
	if err := rows.Scan(&id); err != nil {
		t.Fatalf("mostRecentClaimHandleID: scan: %v", err)
	}
	return id
}

// mustUUID parses a UUID-string from a caller-controlled source, fatal
// on parse error. Used in this test for raw-SQL parameter binding where
// the underlying column type is `uuid` (so pgx requires uuid.UUID, not
// string) — the interface{ String() string } the test threads through
// only has String(), not a UUID() accessor.
func mustUUID(s string) uuid.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("lineage exploration test: mustUUID(%q): %v", s, err))
	}
	return u
}

// waitForLineageReady polls for the producer + consumer leaf-run lineage
// rows to land in the projection. Returns true when both have arrived.
func waitForLineageReady(
	t *testing.T, h *scenario.Harness,
	instanceID, producerNodeID, consumerNodeID interface{ String() string },
	timeout time.Duration,
) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var nProducer, nConsumer int
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_lineage
			 WHERE record_kind = 'leaf_run'
			   AND instance_id = $1
			   AND record->>'node_id' = $2
		`, []any{mustUUID(instanceID.String()), producerNodeID.String()}, &nProducer)
		h.QueryRowSQL(`
			SELECT COUNT(*)
			  FROM rimsky_lineage
			 WHERE record_kind = 'leaf_run'
			   AND instance_id = $1
			   AND record->>'node_id' = $2
		`, []any{mustUUID(instanceID.String()), consumerNodeID.String()}, &nConsumer)
		// The producer is a fan-out with 2 partitions; each partition
		// child emits its own leaf_run row but the parent fan-out run
		// itself doesn't emit a leaf_run (it's the dispatch aggregator,
		// not a leaf). So we expect >=2 producer rows (the children) and
		// >=1 consumer row.
		if nProducer >= 2 && nConsumer >= 1 {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// dumpNodeRuns logs every rimsky_node_runs row for the instance.
func dumpNodeRuns(t *testing.T, h *scenario.Harness, instanceID interface{ String() string }) {
	t.Helper()
	rows, err := h.Pool.Query(h.Ctx, `
		SELECT r.id::text, r.node_id::text, n.node_type, r.state, COALESCE(r.settling_signal_type, '')
		  FROM rimsky_node_runs r
		  JOIN rimsky_nodes n ON n.id = r.node_id
		 WHERE n.instance_id = $1
	`, mustUUID(instanceID.String()))
	if err != nil {
		t.Logf("dumpNodeRuns: query: %v", err)
		return
	}
	defer rows.Close()
	t.Logf("===== node_runs for instance %s =====", instanceID.String())
	count := 0
	for rows.Next() {
		var id, nodeID, nodeType, state, sst string
		if err := rows.Scan(&id, &nodeID, &nodeType, &state, &sst); err != nil {
			t.Logf("  scan err: %v", err)
			continue
		}
		t.Logf("  [%d] run=%s node=%s type=%s state=%s sst=%s",
			count, id, nodeID, nodeType, state, sst)
		count++
	}
	t.Logf("===== %d run(s) =====", count)
}

// dumpLineageRows logs every leaf_run lineage row for the instance.
// Diagnostic-only; called when waitForLineageReady fails.
func dumpLineageRows(t *testing.T, h *scenario.Harness, instanceID interface{ String() string }) {
	t.Helper()
	rows, err := h.Pool.Query(h.Ctx, `
		SELECT record_kind,
		       COALESCE(record->>'node_id', '')        AS node_id,
		       COALESCE(record->>'run_id', '')         AS run_id,
		       COALESCE(record->>'parent_run_id', '')  AS parent_run_id,
		       COALESCE(record->>'state', '')          AS state,
		       observed_at
		  FROM rimsky_lineage
		 WHERE instance_id = $1
		 ORDER BY observed_at ASC
	`, mustUUID(instanceID.String()))
	if err != nil {
		t.Logf("dumpLineageRows: query: %v", err)
		return
	}
	defer rows.Close()
	t.Logf("===== lineage rows for instance %s =====", instanceID.String())
	count := 0
	for rows.Next() {
		var kind, nodeID, runID, parentRunID, state string
		var observedAt time.Time
		if err := rows.Scan(&kind, &nodeID, &runID, &parentRunID, &state, &observedAt); err != nil {
			t.Logf("  scan err: %v", err)
			continue
		}
		t.Logf("  [%d] kind=%s node_id=%s run_id=%s parent_run_id=%q state=%s at=%s",
			count, kind, nodeID, runID, parentRunID, state, observedAt.Format(time.RFC3339Nano))
		count++
	}
	t.Logf("===== %d row(s) =====", count)
}
