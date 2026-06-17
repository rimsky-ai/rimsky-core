// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// asset_management_e2e_test.go — executable proof for STORY-asset-management.
//
// Post-spec the asset surface is observation-and-governance only:
// list, single-detail, version history, materialization history,
// delete. Re-materialization is expressed through messages — the
// empty-message trigger (whole-instance re-run) or a typed message a
// template author designs for partial paths. The materialize-trigger
// endpoint retired (per decision:asset-materialize-endpoint-retired);
// this proof MUST NOT assert it.
//
// Two scenarios share the all-in-one stack:
//
//  1. TestStory_AssetManagement_ObservationSurface — exercises the
//     observation legs end-to-end through the real control-api:
//     deploys a template with a durable-claim producer node, drives a
//     real dispatch via the harness's empty-message wake, waits for the
//     durable claim handle to Promote to committed, then hits each
//     surfaced endpoint:
//        GET    /v1/instances/{id}/assets
//        GET    /v1/instances/{id}/assets/{alias}
//        GET    /v1/instances/{id}/assets/{alias}/versions
//        GET    /v1/instances/{id}/assets/{alias}/materialization-history
//        DELETE /v1/instances/{id}/assets/{alias}
//     The materialization-history assertion pins that the rows match
//     real dispatches (the spec's falsifier shape: "returns rows that
//     don't match what really materialized").
//
//  2. TestStory_AssetManagement_ReMaterializationViaMessage — the
//     explicit-re-materialization-via-message variant: deploys a
//     similar template, drives an initial dispatch, captures the
//     baseline materialization-history rowcount, then posts a SECOND
//     empty message to the instance and asserts the producer dispatches
//     again and a new materialization-history row appears as a result
//     of that real dispatch. The wake is message-driven (not endpoint-
//     driven); the trigger is the universal POST /messages surface.
//
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
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestStory_AssetManagement_ObservationSurface drives the post-spec
// asset surface — list / single / versions / materialization-history /
// delete — through the real control-api against a real durable claim
// handle materialized by a producer node. The materialize endpoint is
// NOT asserted (it retired per the spec).
func TestStory_AssetManagement_ObservationSurface(t *testing.T) {
	t.Parallel()

	// @deliberate: Remote stub store advertising the data_processing
	// protocol so the supervisor dials its DataProcessing surface (the
	// stub fixture enables it). The asset endpoints filter to producers
	// that advertise data_processing, so without the declaration the
	// surfaced asset rows would be filtered out by
	// buildDataProcessingPredicate.
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
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

	// @deliberate: producer settles with no attribute delta — the asset
	// surface reads from the durable claim handle, not the executor
	// payload.
	h.Stub.WhenType("producer").Success(map[string]any{}, true, "produced")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "asset-management-observation", Version: "v1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "producer", Executor: "stub"},
				scenario.WithStores(node.NodeStoreRef{
					Name:     "content",
					Selector: "/asset-management/observation",
					Intent:   "rw",
					Alias:    "dataset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})

	// @constraint: CreateInstance now emits an internal empty-message wake
	// after the POST per
	// decision:test-harness-create-instance-wakes-roots-after-create —
	// driving the structural-root producer to dispatch. The producer's
	// durable claim then Promotes to committed via auto-terminal.
	iid := h.CreateInstance(tid, "ck-asset-management-observation", map[string]any{})
	producerNode := h.FindNode(iid, "producer")
	require.NotNil(t, producerNode, "producer node row must exist")

	require.True(t, h.WaitForNodeState(producerNode.ID, cascade.NodeStateFresh, 30*time.Second),
		"producer must settle fresh via the empty-message wake")
	requireDurableCommitted(t, h, producerNode.ID)

	const assetAlias = "producer.dataset"
	assetsBaseURL := h.ControlBase + "/v1/instances/" + iid.String() + "/assets"

	// @constraint: GET /assets — the surfaced list includes the durable
	// claim alias with its current version_id and state=committed.
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

	// @constraint: GET /assets/{alias} — single-detail surface.
	detailBody := getJSONMap(t, assetsBaseURL+"/"+assetAlias)
	require.Equal(t, assetAlias, detailBody["alias"],
		"GET /assets/{alias} must surface the requested alias")
	require.Equal(t, first["claim_id"], detailBody["claim_id"],
		"single-detail surface must agree with the list surface on claim_id")

	// @constraint: GET /assets/{alias}/versions — version history (via
	// the DataProcessing proxy). The stub store advertises
	// data_processing; the proxy must dial it and return a well-formed
	// `versions` array. The producer here is a non-fan-out single
	// acquirer (no BeginCandidate / CommitCandidate fires), so the
	// fixture's per-claim_handle versions slice is legitimately empty;
	// the surface MUST still return 200 with a well-shaped (possibly
	// empty) versions array. The richer per-version assertions live in
	// fan-out scenarios where the runtime actually drives candidate
	// commit.
	versionsBody := getJSONMap(t, assetsBaseURL+"/"+assetAlias+"/versions")
	versions, hasVersions := versionsBody["versions"].([]any)
	require.Truef(t, hasVersions,
		"GET /assets/{alias}/versions must return a JSON object with a `versions` array; got %v", versionsBody)
	_ = versions

	// @constraint: GET /assets/{alias}/materialization-history — the
	// claim_terminal lineage rows the runtime emitted at every Commit /
	// Abandon / Release. The producer ran exactly once at this point so
	// at least one row must exist (the spec's falsifier shape: rows
	// that don't match what really materialized).
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

	// @constraint: cross-check the materialization-history surface
	// against the raw claim_terminal lineage rows the runtime persisted
	// — guards against the falsifier "returns rows that don't match
	// what really materialized" by reading the audit log directly. The
	// per-asset surface filters to one claim_handle so its row count
	// equals the persisted claim_terminal count for the same asset's
	// claim handle id.
	preCommittedCount := countCommittedLineageRows(t, h, iid)
	require.GreaterOrEqualf(t, preCommittedCount, 1,
		"at least one committed claim_terminal lineage row must exist after the producer's real dispatch; got %d", preCommittedCount)
	require.Equal(t, len(history), preCommittedCount,
		"materialization-history per-asset row count must equal the persisted claim_terminal committed-row count for this instance "+
			"(the asset is the sole producer here, so the per-claim filter matches the per-instance count)")

	// @constraint: DELETE /assets/{alias} — operator-driven retire.
	delResp := httpDelete(t, assetsBaseURL+"/"+assetAlias)
	require.Equalf(t, http.StatusOK, delResp.status,
		"DELETE /assets/{alias} must return 200: %s", string(delResp.raw))
	var delOut map[string]any
	require.NoError(t, json.Unmarshal(delResp.raw, &delOut))
	require.Equal(t, true, delOut["deleted"],
		"DELETE response must report deleted=true")

	// @constraint: alias is gone from the list surface after delete.
	listAfter := getJSONMap(t, assetsBaseURL)
	itemsAfter, _ := listAfter["assets"].([]any)
	require.Lenf(t, itemsAfter, 0,
		"after DELETE the alias must no longer appear on GET /assets; got %d items", len(itemsAfter))
}

// TestStory_AssetManagement_ReMaterializationViaMessage exhibits the
// re-materialization-via-message variant: post a second empty-message
// wake to the instance and observe the producer dispatch again with a
// new materialization-history row recording the second commit. The
// trigger is message-driven; the retired materialize endpoint is not
// exercised.
func TestStory_AssetManagement_ReMaterializationViaMessage(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
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
				scenario.WithStores(node.NodeStoreRef{
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

	// @constraint: First dispatch — driven by the harness's post-create
	// empty-message wake. Wait for the producer to reach fresh and for
	// the durable handle to commit.
	require.True(t, h.WaitForNodeState(producerNode.ID, cascade.NodeStateFresh, 30*time.Second),
		"producer must settle fresh on the first dispatch via the harness wake")
	requireDurableCommitted(t, h, producerNode.ID)

	// @constraint: Snapshot the per-instance counts and per-asset
	// materialization-history BEFORE the second wake. The pre-state
	// asserts the first dispatch really happened; the post-state
	// proves the second wake produced a new dispatch (the spec calls
	// this out as the falsifier-bearing shape).
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

	// @deliberate: explicit-re-materialization-via-message. Post a
	// SECOND empty message to the instance via the universal
	// POST /messages surface. The Idempotency-Key MUST be outside the
	// `harness-wake-create-` prefix reserved by the harness's internal
	// wake (per the GoDoc on Harness.CreateInstance) so dedup doesn't
	// silently collapse this re-wake into the original. The trigger
	// is message-driven (not via the retired materialize endpoint) —
	// that's the proof's load-bearing shape.
	h.PostInstanceMessage(iid, "", nil, fmt.Sprintf("rematerialize-%s", t.Name()))

	// @constraint: STORY-asset-management proof — the producer must
	// dispatch AGAIN as a result of the empty-message wake. A new
	// terminal/success event arrives.
	require.True(t,
		waitForCountGreaterThan(t, func() int { return countTerminalSuccessForInstance(t, h, iid) }, preSuccessCount, 30*time.Second),
		"the second empty-message wake must drive the producer to dispatch again — "+
			"a new terminal/success event must arrive (preCount=%d)", preSuccessCount)

	// @constraint: STORY-asset-management proof — a new committed
	// claim_terminal lineage row appears as a result of that real
	// dispatch. The runtime emits one committed claim_terminal per
	// materialization; the new row records the second commit.
	require.True(t,
		waitForCountGreaterThan(t, func() int { return countCommittedLineageRows(t, h, iid) }, preLineageCount, 30*time.Second),
		"the re-dispatch must emit a new committed claim_terminal lineage row "+
			"(preCount=%d)", preLineageCount)

	// @constraint: STORY-asset-management surface check — the
	// materialization-history endpoint as observed by an operator must
	// reflect the new materialization. Re-materializations of a
	// durable claim produce a new claim_handle row per materialization
	// (per the same-node carve-out at `code:lib/runtime/runner_acquire_claims.go`
	// lines 310-329), so the new materialization shows up on GET /assets
	// as an additional asset entry. The materialization-history surface
	// is per-claim-handle, so the FIRST asset's history may stay
	// constant; the NEW asset's history surfaces its own one-row
	// commit. The operator-facing claim of "a new materialization-
	// history row appears as a result of the second wake" is satisfied
	// by the asset-list growth plus the new asset's own non-empty
	// history surface.
	require.True(t, waitForCountGreaterThan(t, func() int {
		body := getJSONMap(t, listURL)
		items, _ := body["assets"].([]any)
		return len(items)
	}, preAssetCount, 30*time.Second),
		"GET /assets must surface an additional asset row after the re-materialization "+
			"(preCount=%d)", preAssetCount)

	postListBody := getJSONMap(t, listURL)
	postAssets, _ := postListBody["assets"].([]any)
	require.Greaterf(t, len(postAssets), preAssetCount,
		"GET /assets row count must grow after the re-materialization (pre=%d post=%d)",
		preAssetCount, len(postAssets))

	// @constraint: walk every surfaced asset's materialization-history
	// and confirm the SUM across all assets matches the persisted
	// claim_terminal committed-row count for the instance — guards
	// against the per-asset surface dropping rows that are actually
	// present in the underlying audit log.
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

// requireDurableCommitted polls until the producer's claim_handle row
// reaches state=committed AND lifetime=durable. The Promote runs
// asynchronously after the holding subgraph completes.
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

// countTerminalSuccessForInstance returns the per-instance count of
// terminal/success events on rimsky_events. Used to detect that a
// real new dispatch happened (a new event row is only persisted by
// the real terminal-resolution path, which only runs after a real
// dispatch lands).
func countTerminalSuccessForInstance(t *testing.T, h *scenario.Harness, instanceID shared.UUID) int {
	t.Helper()
	var n int
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_events
		 WHERE instance_id = $1 AND kind = 'terminal/success'
	`, []any{instanceID}, &n)
	return n
}

// countCommittedLineageRows returns the per-instance count of
// claim_terminal rows whose outcome=committed. Cross-checks the
// materialization-history HTTP surface against the persisted
// audit-log shape (guard against the falsifier "returns rows that
// don't match what really materialized").
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

// waitForCountGreaterThan polls fn until its return value strictly
// exceeds baseline, or times out. Used to absorb the supervisor-tick
// latency between message receipt and the producer dispatch / Commit
// landing.
func waitForCountGreaterThan(t *testing.T, fn func() int, baseline int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() > baseline {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// getJSONMap GETs the URL and decodes the response into a map.
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

// httpDeleteResp captures status + raw body from a DELETE call.
type httpDeleteResp struct {
	status int
	raw    []byte
}

// httpDelete issues a DELETE against the given URL and returns
// (status, raw body).
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
