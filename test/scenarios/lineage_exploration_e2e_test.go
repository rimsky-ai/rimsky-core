// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestLineageExploration(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	const producerName = "lineage-store"

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				producerName: {
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})

	h.Stub.WhenType("producer").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("consumer").Success(map[string]any{"out": "done"}, true, "done")

	const claimAlias = "data"
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "lineage-exploration-e2e", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "producer",
					Executor: "stub",
				},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
				scenario.WithClaimProducers(scenario.AliasedClaimRef(producerName, "/data/root", "rw", claimAlias)),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "consumer",
					Executor: "stub",
				},
				scenario.WithSubscribes(
					tmplspec.SubscriptionEntry{Node: "producer", Type: "terminal/*", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
					tmplspec.SubscriptionEntry{Node: "producer", Type: "attribute/ok/changed", ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
				),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"upstream_ok": map[string]any{
							"type":   "boolean",
							"source": "{{nodes.producer.attribute.ok}}",
						},
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

	waitForLineageReady(t, h, iid, producerNode.ID, consumerNode.ID)

	producerRunID := mostRecentRunID(t, h, iid, producerNode.ID)
	require.NotEqual(t, "", producerRunID,
		"the producer's leaf-run id must be discoverable from the leaf_run projection")

	consumerRunID := mostRecentRunID(t, h, iid, consumerNode.ID)
	require.NotEqual(t, "", consumerRunID,
		"the consumer's leaf-run id must be discoverable from the leaf_run projection")

	require.Eventually(t, func() bool {
		return consumerCitesAProducerRun(t, h, iid, producerNode.ID, consumerRunID)
	}, 30*time.Second, 200*time.Millisecond,
		"the consumer's lineage row must carry a substitution_refs entry citing the producer's run (source_kind=run); without this the ancestor walk has no link to follow")

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

	{
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

	t.Logf("STORY-lineage-exploration GREEN: producer_run_id=%s consumer_run_id=%s claim_handle_id=%s",
		producerRunID, consumerRunID, claimHandleID)
}

func mostRecentRunID(t *testing.T, h *scenario.Harness, instanceID, nodeID interface{ String() string }) string {
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
		t.Fatalf("mostRecentRunID: query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var runID string
	if err := rows.Scan(&runID); err != nil {
		t.Fatalf("mostRecentRunID: scan: %v", err)
	}
	return runID
}

func consumerCitesAProducerRun(
	t *testing.T, h *scenario.Harness,
	instanceID, producerNodeID interface{ String() string },
	consumerRunID string,
) bool {
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

func mustUUID(s string) uuid.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("lineage exploration test: mustUUID(%q): %v", s, err))
	}
	return u
}

func waitForLineageReady(
	t *testing.T, h *scenario.Harness,
	instanceID, producerNodeID, consumerNodeID interface{ String() string },
) {
	t.Helper()
	for {
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
		if nProducer >= 1 && nConsumer >= 1 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
