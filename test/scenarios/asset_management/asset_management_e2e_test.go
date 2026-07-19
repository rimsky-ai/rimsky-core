// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @story: asset-management
// @concept: asset
// @decision: asset-materialize-endpoint-retired
// @decision: empty-message-as-root-trigger

package asset_management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestStory_AssetManagement_ObservationSurface(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint: "grpc://" + endpoint,
					Protocols: []string{
						config.ProtocolClaimProducer,
						claimproducer.ProtocolDataProcessing,
					},
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})

	h.Stub.WhenType("producer").Success(map[string]any{}, true, "produced")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "asset-management-observation", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "producer", Executor: "stub"},
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "content",
					Selector: "/asset-management/observation",
					Intent:   "rw",
					Alias:    "dataset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-asset-management-observation", map[string]any{})
	producerNode := h.FindNode(iid, "producer")
	require.NotNil(t, producerNode, "producer node row must exist")

	h.WaitForNodeState(producerNode.ID, cascade.NodeStateFresh)
	requireDurableCommitted(t, h, producerNode.ID)

	const assetAlias = "producer.dataset"
	assetsBaseURL := h.ControlBase + "/v1/instances/" + iid.String() + "/assets"

	listBody := getJSONMap(t, assetsBaseURL)
	items, _ := listBody["assets"].([]any)
	require.Lenf(t, items, 1,
		"GET /assets must list exactly the one durable claim handle the producer materialized; got %d", len(items))
	first, _ := items[0].(map[string]any)
	require.NotNil(t, first)
	require.Equal(t, assetAlias, first["alias"],
		"the listed alias must be the dotted {node_type}.{claim_alias} form")
	require.Equal(t, "content", first["producer_name"])
	require.Equal(t, "committed", first["state"],
		"asset must surface state=committed (the post-Promote durable state)")
	require.Equal(t, "durable", first["lifetime"],
		"asset must surface lifetime=durable")

	detailBody := getJSONMap(t, assetsBaseURL+"/"+assetAlias)
	require.Equal(t, assetAlias, detailBody["alias"],
		"GET /assets/{alias} must surface the requested alias")
	require.Equal(t, first["claim_id"], detailBody["claim_id"],
		"single-detail surface must agree with the list surface on claim_id")

	versionsBody := getJSONMap(t, assetsBaseURL+"/"+assetAlias+"/versions")
	versions, hasVersions := versionsBody["versions"].([]any)
	require.Truef(t, hasVersions,
		"GET /assets/{alias}/versions must return a JSON object with a `versions` array; got %v", versionsBody)
	require.Emptyf(t, versions,
		"the template deployed here has no fan-out, so no data-processing candidate is ever begun/committed "+
			"for this claim; versions must stay exactly empty rather than surface a stray or misattributed entry; got %v",
		versions)

	historyURL := assetsBaseURL + "/" + assetAlias + "/materialization-history"
	historyBody := getJSONMap(t, historyURL)
	history, _ := historyBody["materialization_history"].([]any)
	require.NotEmptyf(t, history,
		"GET /assets/{alias}/materialization-history must include the Commit lineage row from the producer's real dispatch")
	for i, raw := range history {
		row, _ := raw.(map[string]any)
		require.NotNilf(t, row, "materialization_history[%d] must decode as an object; got %v", i, raw)
		require.Equalf(t, "claim_terminal", row["record_kind"],
			"materialization_history[%d] must surface record_kind=claim_terminal (the only kind joined in); got %v", i, row["record_kind"])
	}

	preCommittedCount := countCommittedLineageRows(t, h, iid)
	require.GreaterOrEqualf(t, preCommittedCount, 1,
		"at least one committed claim_terminal lineage row must exist after the producer's real dispatch; got %d", preCommittedCount)
	require.Equal(t, len(history), preCommittedCount,
		"materialization-history per-asset row count must equal the persisted claim_terminal committed-row count for this instance "+
			"(the asset is the sole producer here, so the per-claim filter matches the per-instance count)")

	delResp := httpDelete(t, assetsBaseURL+"/"+assetAlias)
	require.Equalf(t, http.StatusOK, delResp.status,
		"DELETE /assets/{alias} must return 200: %s", string(delResp.raw))
	var delOut map[string]any
	require.NoError(t, json.Unmarshal(delResp.raw, &delOut))
	require.Equal(t, true, delOut["deleted"],
		"DELETE response must report deleted=true")

	listAfter := getJSONMap(t, assetsBaseURL)
	itemsAfter, _ := listAfter["assets"].([]any)
	require.Lenf(t, itemsAfter, 0,
		"after DELETE the alias must no longer appear on GET /assets; got %d items", len(itemsAfter))
}

func TestStory_AssetManagement_ReMaterializationViaMessage(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint: "grpc://" + endpoint,
					Protocols: []string{
						config.ProtocolClaimProducer,
						claimproducer.ProtocolDataProcessing,
					},
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})
	h.Stub.WhenType("producer").Success(map[string]any{}, true, "produced")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "asset-management-rematerialize", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "producer", Executor: "stub"},
				scenario.WithClaimProducers(node.NodeClaimProducerRef{
					Name:     "content",
					Selector: "/asset-management/rematerialize",
					Intent:   "rw",
					Alias:    "dataset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-asset-management-rematerialize", map[string]any{})
	producerNode := h.FindNode(iid, "producer")
	require.NotNil(t, producerNode, "producer node row must exist")

	h.WaitForNodeState(producerNode.ID, cascade.NodeStateFresh)
	requireDurableCommitted(t, h, producerNode.ID)

	const assetAlias = "producer.dataset"
	assetsBaseURL := h.ControlBase + "/v1/instances/" + iid.String() + "/assets"
	listURL := assetsBaseURL

	preSuccessCount := countTerminalSuccessForInstance(t, h, iid)
	preLineageCount := countCommittedLineageRows(t, h, iid)
	require.GreaterOrEqual(t, preSuccessCount, 1,
		"producer must have at least one terminal/success event after the first dispatch")
	require.GreaterOrEqual(t, preLineageCount, 1,
		"producer's first dispatch must have emitted at least one committed claim_terminal lineage row")

	preListBody := getJSONMap(t, listURL)
	preAssets, _ := preListBody["assets"].([]any)
	preAssetCount := len(preAssets)
	require.GreaterOrEqual(t, preAssetCount, 1,
		"the first dispatch must surface at least one asset on GET /assets")

	preHistoryURL := assetsBaseURL + "/" + assetAlias + "/materialization-history"
	preHistoryBody := getJSONMap(t, preHistoryURL)
	preHistory, _ := preHistoryBody["materialization_history"].([]any)
	require.GreaterOrEqual(t, len(preHistory), 1,
		"the first dispatch must surface at least one materialization-history row on the per-asset surface")

	h.PostInstanceMessage(iid, "", nil, fmt.Sprintf("rematerialize-%s", t.Name()))

	waitForCountGreaterThan(t, func() int { return countTerminalSuccessForInstance(t, h, iid) }, preSuccessCount)

	waitForCountGreaterThan(t, func() int { return countCommittedLineageRows(t, h, iid) }, preLineageCount)

	waitForCountGreaterThan(t, func() int {
		body := getJSONMap(t, listURL)
		items, _ := body["assets"].([]any)
		return len(items)
	}, preAssetCount)

	postListBody := getJSONMap(t, listURL)
	postAssets, _ := postListBody["assets"].([]any)
	require.Greaterf(t, len(postAssets), preAssetCount,
		"GET /assets row count must grow after the re-materialization (pre=%d post=%d)",
		preAssetCount, len(postAssets))

	totalHistory := 0
	for _, raw := range postAssets {
		row, _ := raw.(map[string]any)
		require.NotNil(t, row)
		alias, _ := row["alias"].(string)
		require.NotEmpty(t, alias, "every surfaced asset must carry a non-empty alias")
		hb := getJSONMap(t, assetsBaseURL+"/"+alias+"/materialization-history")
		rows, _ := hb["materialization_history"].([]any)
		totalHistory += len(rows)
	}
	postLineageCount := countCommittedLineageRows(t, h, iid)
	require.Equalf(t, postLineageCount, totalHistory,
		"sum of per-asset materialization-history rowcount must equal the persisted committed claim_terminal count "+
			"(persisted=%d surface-sum=%d)", postLineageCount, totalHistory)
}

func TestStory_AssetManagement_CrossInstanceIsolation(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"content": {
					Endpoint: "grpc://" + endpoint,
					Protocols: []string{
						config.ProtocolClaimProducer,
						claimproducer.ProtocolDataProcessing,
					},
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})
	h.Stub.WhenType("producer").Success(map[string]any{}, true, "produced")

	deployForSelector := func(name, selector string) string {
		return h.DeployTemplate(node.TemplateSpec{
			Name: name, Version: "v1",
			Nodes: []node.TemplateNodeDef{
				scenario.MakeNode(
					node.TemplateNodeDef{Type: "producer", Executor: "stub"},
					scenario.WithClaimProducers(node.NodeClaimProducerRef{
						Name:     "content",
						Selector: selector,
						Intent:   "rw",
						Alias:    "dataset",
						Lifetime: string(spec.ClaimLifetimeDurable),
					}),
				),
			},
		})
	}
	tidA := deployForSelector("asset-management-cross-instance-a", "/asset-management/cross-instance-a")
	tidB := deployForSelector("asset-management-cross-instance-b", "/asset-management/cross-instance-b")

	iidA := h.CreateInstance(tidA, "ck-asset-management-cross-instance-a", map[string]any{})
	iidB := h.CreateInstance(tidB, "ck-asset-management-cross-instance-b", map[string]any{})

	nodeA := h.FindNode(iidA, "producer")
	nodeB := h.FindNode(iidB, "producer")
	require.NotNil(t, nodeA, "producer node row must exist for instance A")
	require.NotNil(t, nodeB, "producer node row must exist for instance B")

	h.WaitForNodeState(nodeA.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(nodeB.ID, cascade.NodeStateFresh)
	requireDurableCommitted(t, h, nodeA.ID)
	requireDurableCommitted(t, h, nodeB.ID)

	const assetAlias = "producer.dataset"
	baseA := h.ControlBase + "/v1/instances/" + iidA.String() + "/assets"
	baseB := h.ControlBase + "/v1/instances/" + iidB.String() + "/assets"

	listA := getJSONMap(t, baseA)
	itemsA, _ := listA["assets"].([]any)
	require.Lenf(t, itemsA, 1, "GET /assets for instance A must list only instance A's asset; got %d", len(itemsA))
	claimA, _ := itemsA[0].(map[string]any)
	require.NotNil(t, claimA)

	listB := getJSONMap(t, baseB)
	itemsB, _ := listB["assets"].([]any)
	require.Lenf(t, itemsB, 1, "GET /assets for instance B must list only instance B's asset; got %d", len(itemsB))
	claimB, _ := itemsB[0].(map[string]any)
	require.NotNil(t, claimB)

	require.NotEqual(t, claimA["claim_id"], claimB["claim_id"],
		"instance A and instance B must materialize distinct claim handles for the same alias")

	detailA := getJSONMap(t, baseA+"/"+assetAlias)
	require.Equal(t, claimA["claim_id"], detailA["claim_id"],
		"GET /assets/{alias} scoped to instance A must resolve instance A's claim, not instance B's")

	detailB := getJSONMap(t, baseB+"/"+assetAlias)
	require.Equal(t, claimB["claim_id"], detailB["claim_id"],
		"GET /assets/{alias} scoped to instance B must resolve instance B's claim, not instance A's")

	delResp := httpDelete(t, baseA+"/"+assetAlias)
	require.Equalf(t, http.StatusOK, delResp.status,
		"DELETE on instance A's asset must succeed: %s", string(delResp.raw))

	listAAfter := getJSONMap(t, baseA)
	itemsAAfter, _ := listAAfter["assets"].([]any)
	require.Lenf(t, itemsAAfter, 0, "instance A's asset must be gone after DELETE; got %d", len(itemsAAfter))

	listBAfter := getJSONMap(t, baseB)
	itemsBAfter, _ := listBAfter["assets"].([]any)
	require.Lenf(t, itemsBAfter, 1,
		"deleting instance A's asset must not affect instance B's asset under the same alias; got %d", len(itemsBAfter))
	claimBAfter, _ := itemsBAfter[0].(map[string]any)
	require.NotNil(t, claimBAfter)
	require.Equal(t, claimB["claim_id"], claimBAfter["claim_id"],
		"instance B's claim must be untouched by instance A's delete")
}

func requireDurableCommitted(t *testing.T, h *scenario.Harness, producerNodeID shared.UUID) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last *persistence.ClaimHandleRow
	for time.Now().Before(deadline) {
		var rows []persistence.ClaimHandleRow
		require.NoError(t, h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.ClaimHandles().ListByHolderNode(ctx, producerNodeID, tx)
			rows = r
			return err
		}))
		for i := range rows {
			r := &rows[i]
			if r.Lifetime == spec.ClaimLifetimeDurable {
				last = r
				if r.State == spec.ClaimHandleStateCommitted {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if last == nil {
		require.Fail(t, "no durable claim_handle row found for producer", "node_id=%s", producerNodeID)
		return
	}
	require.Failf(t, "durable claim_handle did not reach committed",
		"last seen state=%s lifetime=%s", last.State, last.Lifetime)
}

func countTerminalSuccessForInstance(t *testing.T, h *scenario.Harness, instanceID shared.UUID) int {
	t.Helper()
	var n int
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_events
		 WHERE instance_id = $1 AND kind = 'terminal/success'
	`, []any{instanceID}, &n)
	return n
}

func countCommittedLineageRows(t *testing.T, h *scenario.Harness, instanceID shared.UUID) int {
	t.Helper()
	var n int
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_lineage
		 WHERE instance_id = $1
		   AND record_kind = 'claim_terminal'
		   AND outcome = 'committed'
	`, []any{instanceID}, &n)
	return n
}

func waitForCountGreaterThan(t *testing.T, fn func() int, baseline int) {
	t.Helper()
	for fn() <= baseline {
		time.Sleep(50 * time.Millisecond)
	}
}

func getJSONMap(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s: status=%d body=%s", url, resp.StatusCode, string(raw))
	var out map[string]any
	require.NoErrorf(t, json.Unmarshal(raw, &out), "GET %s: decode: %s", url, string(raw))
	return out
}

type httpDeleteResp struct {
	status int
	raw    []byte
}

func httpDelete(t *testing.T, url string) httpDeleteResp {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return httpDeleteResp{status: resp.StatusCode, raw: raw}
}
