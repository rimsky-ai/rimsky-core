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
		Deadline: 180 * time.Second,
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
				scenario.WithClaimProducers(scenario.AliasedClaimRef(producerName, "/data/root", "rw", claimAlias)),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "consumer",
					Executor: "stub",
				},
				scenario.WithSubscribes(
					tmplspec.SubscriptionEntry{Node: "producer", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
					tmplspec.SubscriptionEntry{Node: "producer", Type: "attribute/ok/changed", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)},
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

	if !waitForLineageReady(t, h, iid, producerNode.ID, consumerNode.ID, 90*time.Second) {
		t.Logf("producer node_id = %s", producerNode.ID.String())
		t.Logf("consumer node_id = %s", consumerNode.ID.String())
		dumpNodeRuns(t, h, iid)
		dumpLineageRows(t, h, iid)
		t.Fatalf("the real assembled product must write leaf_run lineage rows for the producer (>=2 partition children) and the consumer (>=1 row); see dumped rows above")
	}

	producerParentRunID := mostRecentProducerParentRunID(t, h, iid, producerNode.ID)
	require.NotEqual(t, "", producerParentRunID,
		"the producer's parent fan-out run-id must be discoverable from the leaf_run projection")

	consumerRunID := mostRecentConsumerRunID(t, h, iid, consumerNode.ID)
	require.NotEqual(t, "", consumerRunID,
		"the consumer's leaf-run id must be discoverable from the leaf_run projection")

	require.Eventually(t, func() bool {
		return consumerCitesAProducerRun(t, h, iid, producerNode.ID, consumerRunID)
	}, 30*time.Second, 200*time.Millisecond,
		"the consumer's lineage row must carry a substitution_refs entry citing one of the producer's runs (source_kind=run); without this the ancestor walk has no link to follow")

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
		url := h.ControlBase + "/v1/lineage/runs/" + producerParentRunID + "/descendants?depth=3"
		status, body := httpGetJSON(t, url)
		require.Equal(t, http.StatusOK, status, "GET descendants: %s", body)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		descendants, ok := out["descendants"].([]any)
		require.True(t, ok, "descendants array present")
		require.GreaterOrEqual(t, len(descendants), 2,
			"descendants of producer parent must include the >=2 real fan-out child runs (one per partition); falsifier brief: 'a real downstream consumer is missing from the descendant walk'")
		for _, d := range descendants {
			item, ok := d.(map[string]any)
			require.True(t, ok, "descendant item is object")
			rec, ok := item["record"].(map[string]any)
			require.True(t, ok, "descendant record present")
			require.Equal(t, producerParentRunID, rec["parent_run_id"],
				"each descendant row must cite the seed as its parent_run_id")
		}
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

	t.Logf("STORY-lineage-exploration GREEN: producer_parent_run_id=%s consumer_run_id=%s claim_handle_id=%s",
		producerParentRunID, consumerRunID, claimHandleID)
	dumpLineageRows(t, h, iid)
}

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
		if nProducer >= 2 && nConsumer >= 1 {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

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
