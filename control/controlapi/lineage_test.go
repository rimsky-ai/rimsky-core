// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// lineage_test.go — F6 integration tests for the lineage handlers
// against the pgtest harness (httptest server + real postgres). Drives
// the descendant walker end-to-end so the QueryByParentRunID + BFS
// composition is exercised against the real predicate
// (`record->>'parent_run_id' = $1`) rather than the in-memory fake.

package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// TestLineageRunDescendants_HandlerWalksChain seeds a lineage chain
// (root → child1 → grandchild1) via direct table writes and verifies
// `GET /lineage/runs/{root}/descendants?depth=2` returns the two
// downstream rows. The seed bypasses the runtime emission path because
// this test targets the handler's BFS + QueryByParentRunID composition,
// not the writer wiring.
func TestLineageRunDescendants_HandlerWalksChain(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	// Seed a deployed template + instance so the FK chain on the
	// lineage rows is satisfiable. The runtime path doesn't read
	// `rimsky_lineage.instance_id` for the descendant walker, but the
	// row's NOT NULL constraint requires a real instance row.
	tplBody := validTemplateBody("lin-desc-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "lin-desc-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	// Re-use the instance's seeded frame so the lineage rows FK against
	// a real frame_id.
	var frameID uuid.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instUUID), tx)
		if err != nil || len(nodes) == 0 {
			return err
		}
		// Any seeded frame for this instance works.
		fid, err := h.persist.Frames().EnqueueSerialFrame(ctx, shared.UUID(instUUID), nodes[0].ID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	require.NotEqual(t, uuid.Nil, frameID)

	// Build the chain. Root has no parent; child1 → root; grandchild1 → child1.
	rootRunID := uuid.New()
	child1RunID := uuid.New()
	grandchild1RunID := uuid.New()

	insertLeafRun := func(t *testing.T, runID, parentRunID uuid.UUID, observedAt time.Time) {
		t.Helper()
		rec := map[string]any{
			"run_id":               runID.String(),
			"frame_id":             frameID.String(),
			"state":                "fresh",
			"settling_signal_type": "terminal/success",
		}
		if parentRunID != uuid.Nil {
			rec["parent_run_id"] = parentRunID.String()
		}
		recBytes, err := json.Marshal(rec)
		require.NoError(t, err)
		require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
				ID:         shared.UUID(uuid.New()),
				RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: shared.UUID(instUUID),
				FrameID:    shared.UUID(frameID),
				ObservedAt: observedAt,
				Record:     recBytes,
			})
		}))
	}
	base := time.Now().UTC()
	insertLeafRun(t, rootRunID, uuid.Nil, base)
	insertLeafRun(t, child1RunID, rootRunID, base.Add(1*time.Second))
	insertLeafRun(t, grandchild1RunID, child1RunID, base.Add(2*time.Second))

	// Walk descendants depth=2 from root.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/lineage/runs/%s/descendants?depth=2", rootRunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 2, out["depth"])
	descendants, _ := out["descendants"].([]any)
	require.Len(t, descendants, 2, "expected child1 + grandchild1 in the descendant set")

	// Verify child1 + grandchild1 are present (order is observed_at ASC
	// per the per-frontier query, but the handler does BFS so the
	// guarantee is set-membership, not ordering).
	gotIDs := make(map[string]bool, len(descendants))
	for _, d := range descendants {
		item := d.(map[string]any)
		rawRecord, ok := item["record"].(map[string]any)
		require.True(t, ok, "record field present")
		runID, _ := rawRecord["run_id"].(string)
		require.NotEmpty(t, runID)
		gotIDs[runID] = true
	}
	require.True(t, gotIDs[child1RunID.String()], "child1 missing from descendants: %v", gotIDs)
	require.True(t, gotIDs[grandchild1RunID.String()], "grandchild1 missing from descendants: %v", gotIDs)
	require.False(t, gotIDs[rootRunID.String()], "root must not appear in its own descendants set")

	// Bonus: depth=1 must surface only child1.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/lineage/runs/%s/descendants?depth=1", rootRunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	descendants, _ = out["descendants"].([]any)
	require.Len(t, descendants, 1, "depth=1 should only return child1")
}

// TestLineageRunAncestors_HandlerWalksChain seeds a lineage chain
// (root → child1 → grandchild1) via direct table writes, where each
// node's `substitution_refs` cites the upstream run id, and verifies
// `GET /lineage/runs/{grandchild1}/ancestors?depth=2` returns the two
// upstream rows. Mirrors the descendants test: the seed must NOT
// appear in its own ancestors set.
func TestLineageRunAncestors_HandlerWalksChain(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	tplBody := validTemplateBody("lin-anc-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": "lin-anc-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	var frameID uuid.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instUUID), tx)
		if err != nil || len(nodes) == 0 {
			return err
		}
		fid, err := h.persist.Frames().EnqueueSerialFrame(ctx, shared.UUID(instUUID), nodes[0].ID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	require.NotEqual(t, uuid.Nil, frameID)

	// Build the chain. Root has no substitution_refs; child1 cites root;
	// grandchild1 cites child1. (The walker queries from grandchild1
	// toward root, so refs point upstream.)
	rootRunID := uuid.New()
	child1RunID := uuid.New()
	grandchild1RunID := uuid.New()

	insertLeafRun := func(t *testing.T, runID uuid.UUID, parentRunID uuid.UUID, observedAt time.Time) {
		t.Helper()
		rec := map[string]any{
			"run_id":               runID.String(),
			"frame_id":             frameID.String(),
			"state":                "fresh",
			"settling_signal_type": "terminal/success",
		}
		if parentRunID != uuid.Nil {
			// Cite the upstream run as a substitution_ref so the
			// ancestor walker can follow the chain.
			rec["substitution_refs"] = []map[string]any{
				{
					"source_kind":          "run",
					"source_version_or_id": parentRunID.String(),
				},
			}
		}
		recBytes, err := json.Marshal(rec)
		require.NoError(t, err)
		require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
				ID:         shared.UUID(uuid.New()),
				RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: shared.UUID(instUUID),
				FrameID:    shared.UUID(frameID),
				ObservedAt: observedAt,
				Record:     recBytes,
			})
		}))
	}
	base := time.Now().UTC()
	insertLeafRun(t, rootRunID, uuid.Nil, base)
	insertLeafRun(t, child1RunID, rootRunID, base.Add(1*time.Second))
	insertLeafRun(t, grandchild1RunID, child1RunID, base.Add(2*time.Second))

	// Walk ancestors depth=2 from grandchild1.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/lineage/runs/%s/ancestors?depth=2", grandchild1RunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 2, out["depth"])
	ancestors, _ := out["ancestors"].([]any)
	require.Len(t, ancestors, 2, "expected root + child1 in the ancestor set")

	gotIDs := make(map[string]bool, len(ancestors))
	for _, a := range ancestors {
		item := a.(map[string]any)
		rawRecord, ok := item["record"].(map[string]any)
		require.True(t, ok, "record field present")
		runID, _ := rawRecord["run_id"].(string)
		require.NotEmpty(t, runID)
		gotIDs[runID] = true
	}
	require.True(t, gotIDs[child1RunID.String()], "child1 missing from ancestors: %v", gotIDs)
	require.True(t, gotIDs[rootRunID.String()], "root missing from ancestors: %v", gotIDs)
	require.False(t, gotIDs[grandchild1RunID.String()], "seed (grandchild1) must not appear in its own ancestors set")

	// Bonus: depth=1 must surface only child1.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/lineage/runs/%s/ancestors?depth=1", grandchild1RunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	ancestors, _ = out["ancestors"].([]any)
	require.Len(t, ancestors, 1, "depth=1 should only return child1")
}
