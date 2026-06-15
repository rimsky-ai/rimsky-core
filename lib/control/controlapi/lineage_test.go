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
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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

	// @constraint: seed a deployed template + instance so the FK chain on the
	// lineage rows is satisfiable. The runtime path doesn't read
	// `rimsky_lineage.instance_id` for the descendant walker, but the
	// row's NOT NULL constraint requires a real instance row.
	tplBody := validTemplateBody("lin-desc-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "lin-desc-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instID, _ := out["instance_id"].(string)
	instUUID, err := uuid.Parse(instID)
	require.NoError(t, err)

	// @constraint: re-use the instance's seeded frame so the lineage rows FK
	// against a real frame_id.
	var frameID uuid.UUID
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instUUID), tx)
		if err != nil || len(nodes) == 0 {
			return err
		}
		// @constraint: any seeded frame for this instance works.
		fid, err := h.persist.Frames().EnqueueSerialFrame(ctx, shared.UUID(instUUID), nodes[0].ID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	require.NotEqual(t, uuid.Nil, frameID)

	// @constraint: build the chain. Root has no parent; child1 → root; grandchild1 → child1.
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

	// @constraint: walk descendants depth=2 from root.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/descendants?depth=2", rootRunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 2, out["depth"])
	descendants, _ := out["descendants"].([]any)
	require.Len(t, descendants, 2, "expected child1 + grandchild1 in the descendant set")

	// @constraint: verify child1 + grandchild1 are present (order is observed_at ASC
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

	// @constraint: bonus: depth=1 must surface only child1.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/descendants?depth=1", rootRunID.String()), nil)
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
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
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

	// @constraint: build the chain. Root has no substitution_refs; child1 cites root;
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
			// @constraint: cite the upstream run as a substitution_ref so the
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

	// @constraint: walk ancestors depth=2 from grandchild1.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/ancestors?depth=2", grandchild1RunID.String()), nil)
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

	// @constraint: bonus: depth=1 must surface only child1.
	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/ancestors?depth=1", grandchild1RunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	ancestors, _ = out["ancestors"].([]any)
	require.Len(t, ancestors, 1, "depth=1 should only return child1")
}

// TestLineagePrune_DryRunCountMatchesLiveDelete drives handleLineagePrune
// directly (the harness wires AuthState=nil, so the gateByAction that
// resolves `?dry_run=true` into the request mode doesn't run; we set the
// mode on the request context instead). It seeds prunable + non-prunable
// lineage rows against the real postgres harness, then asserts the
// dry-run `count` exactly equals the live `deleted` for the same cutoff
// — proving CountOlderThan previews DeleteOlderThan rather than
// approximating it.
func TestLineagePrune_DryRunCountMatchesLiveDelete(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	// @constraint: seed a deployed template + instance so the lineage rows' instance_id
	// FK is satisfiable.
	tplBody := validTemplateBody("lin-prune-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": "lin-prune-ck-" + uuid.NewString(),
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

	insertLeafRun := func(t *testing.T, observedAt time.Time) {
		t.Helper()
		// @constraint: A random run_id with no matching rimsky_node_runs row, so the
		// prune predicate's NOT EXISTS(run) half is satisfied.
		rec := map[string]any{
			"run_id":               uuid.NewString(),
			"frame_id":             frameID.String(),
			"state":                "fresh",
			"settling_signal_type": "terminal/success",
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

	now := time.Now().UTC()
	cutoff := now.Add(-1 * time.Hour)
	before := cutoff.Format(time.RFC3339)

	// @constraint: three prunable rows (older than cutoff, no live run) + one too-recent
	// row (must be spared by both dry-run count and live delete).
	insertLeafRun(t, now.Add(-4*time.Hour))
	insertLeafRun(t, now.Add(-3*time.Hour))
	insertLeafRun(t, now.Add(-2*time.Hour))
	insertLeafRun(t, now.Add(-1*time.Minute))
	wantPrunable := 3

	deps := AppDeps{
		Persist: h.persist,
		Logger:  shared.SilentLogger{},
		Clock:   shared.SystemClock{},
	}
	handler := handleLineagePrune(deps)

	// @constraint: dry-run: mode set on the context (no gateByAction in this harness).
	dryReq := httptest.NewRequest(http.MethodPost, "/v1/admin/lineage/prune",
		strings.NewReader(`{"before":"`+before+`"}`))
	dryReq = dryReq.WithContext(context.WithValue(dryReq.Context(), ctxKeyMode{}, auth.ModeDryRun))
	dryRec := httptest.NewRecorder()
	handler(dryRec, dryReq)
	require.Equal(t, http.StatusOK, dryRec.Code, dryRec.Body.String())

	var dryBody struct {
		DryRun          bool `json:"dry_run"`
		WouldHavePruned struct {
			Before string `json:"before"`
			Count  int    `json:"count"`
		} `json:"would_have_pruned"`
	}
	require.NoError(t, json.Unmarshal(dryRec.Body.Bytes(), &dryBody))
	require.True(t, dryBody.DryRun, "dry_run flag must be set")
	require.Equal(t, before, dryBody.WouldHavePruned.Before)
	require.Equal(t, wantPrunable, dryBody.WouldHavePruned.Count, "dry-run count must equal the prunable rows")

	// @constraint: live delete with the SAME cutoff (default mode = execute).
	liveReq := httptest.NewRequest(http.MethodPost, "/v1/admin/lineage/prune",
		strings.NewReader(`{"before":"`+before+`"}`))
	liveRec := httptest.NewRecorder()
	handler(liveRec, liveReq)
	require.Equal(t, http.StatusOK, liveRec.Code, liveRec.Body.String())

	var liveBody struct {
		Deleted int    `json:"deleted"`
		Before  string `json:"before"`
	}
	require.NoError(t, json.Unmarshal(liveRec.Body.Bytes(), &liveBody))
	require.Equal(t, before, liveBody.Before)

	// @constraint: the contract: dry-run count == live deleted for the same cutoff.
	require.Equal(t, dryBody.WouldHavePruned.Count, liveBody.Deleted,
		"dry-run preview must equal the live delete count")
}

// seedLineageInstance deploys a template + instance and returns the
// instance id and an enqueued frame id so seeded lineage rows satisfy the
// instance_id FK. Shared by the TestLineageEndpoints_* characterization
// tests below.
func seedLineageInstance(t *testing.T, h *harness, prefix string) (instID uuid.UUID, frameID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	tplBody := validTemplateBody(prefix + "-" + uuid.NewString())
	_, out := h.httpJSON(t, "POST", "/v1/templates", tplBody)
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": prefix + "-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instStr, _ := out["instance_id"].(string)
	var err error
	instID, err = uuid.Parse(instStr)
	require.NoError(t, err)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instID), tx)
		if err != nil || len(nodes) == 0 {
			return err
		}
		fid, err := h.persist.Frames().EnqueueSerialFrame(ctx, shared.UUID(instID), nodes[0].ID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = uuid.UUID(fid)
		return nil
	}))
	require.NotEqual(t, uuid.Nil, frameID)
	return instID, frameID
}

// insertLineageRow is a thin direct-insert helper for the
// TestLineageEndpoints_* tests. recordKind is leaf_run | claim_terminal;
// record is the per-kind JSONB payload.
func insertLineageRow(t *testing.T, h *harness, instID, frameID uuid.UUID, recordKind string, record map[string]any, observedAt time.Time, outcome string) {
	t.Helper()
	ctx := context.Background()
	recBytes, err := json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
			ID:         shared.UUID(uuid.New()),
			RecordKind: recordKind,
			InstanceID: shared.UUID(instID),
			FrameID:    shared.UUID(frameID),
			ObservedAt: observedAt,
			Record:     recBytes,
			Outcome:    outcome,
		})
	}))
}

// TestLineageEndpoints_RunReturnsMostRecent seeds two leaf_run rows for the
// same run_id and asserts GET /lineage/runs/{run_id} returns the most
// recent (observed_at DESC) record; an unknown run_id returns 404.
func TestLineageEndpoints_RunReturnsMostRecent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID, frameID := seedLineageInstance(t, h, "lin-ep-run")
	runID := uuid.New()
	base := time.Now().UTC()
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindLeafRun,
		map[string]any{"run_id": runID.String(), "frame_id": frameID.String(), "state": "fresh", "attempt": 1}, base, "")
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindLeafRun,
		map[string]any{"run_id": runID.String(), "frame_id": frameID.String(), "state": "fresh", "attempt": 2}, base.Add(time.Second), "")

	status, out := h.httpJSON(t, "GET", "/v1/lineage/runs/"+runID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "leaf_run", out["record_kind"])
	record, _ := out["record"].(map[string]any)
	require.Equal(t, runID.String(), record["run_id"])
	require.EqualValues(t, 2, record["attempt"], "GET /lineage/runs returns the most recent (observed_at ASC, last) record")

	// @constraint: unknown run → 404.
	status, _ = h.httpJSON(t, "GET", "/v1/lineage/runs/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNotFound, status)

	// @constraint: malformed run id → 400.
	status, _ = h.httpJSON(t, "GET", "/v1/lineage/runs/not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

// TestLineageEndpoints_ClaimReturnsMostRecent seeds two claim_terminal rows
// for the same claim_handle_id and asserts GET
// /lineage/claims/{claim_handle_id} returns the most recent record.
func TestLineageEndpoints_ClaimReturnsMostRecent(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID, frameID := seedLineageInstance(t, h, "lin-ep-claim")
	claimID := uuid.New()
	base := time.Now().UTC()
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": claimID.String(), "version_id": "v-001"}, base, persistence.LineageOutcomeCommitted)
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": claimID.String(), "version_id": "v-002"}, base.Add(time.Second), persistence.LineageOutcomeCommitted)

	status, out := h.httpJSON(t, "GET", "/v1/lineage/claims/"+claimID.String(), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, "claim_terminal", out["record_kind"])
	record, _ := out["record"].(map[string]any)
	require.Equal(t, "v-002", record["version_id"], "GET /lineage/claims returns the most recent record")

	status, _ = h.httpJSON(t, "GET", "/v1/lineage/claims/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNotFound, status)
}

// TestLineageEndpoints_ClaimAncestorsWalksSubClaimChain seeds a parent
// claim_terminal whose record cites two sub_claim_handle_ids, each of which
// has its own claim_terminal row, and asserts GET
// /lineage/claims/{parent}/ancestors walks the sub-claim chain.
func TestLineageEndpoints_ClaimAncestorsWalksSubClaimChain(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID, frameID := seedLineageInstance(t, h, "lin-ep-claimanc")
	parentClaim := uuid.New()
	subA := uuid.New()
	subB := uuid.New()
	base := time.Now().UTC()

	// @constraint: parent cites two sub-claims; each sub-claim has its own terminal row.
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{
			"claim_handle_id":      parentClaim.String(),
			"sub_claim_handle_ids": []string{subA.String(), subB.String()},
		}, base, persistence.LineageOutcomeCommitted)
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": subA.String(), "version_id": "sub-a"}, base.Add(time.Second), persistence.LineageOutcomeCommitted)
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": subB.String(), "version_id": "sub-b"}, base.Add(2*time.Second), persistence.LineageOutcomeCommitted)

	status, out := h.httpJSON(t, "GET", "/v1/lineage/claims/"+parentClaim.String()+"/ancestors?depth=2", nil)
	require.Equal(t, http.StatusOK, status, out)
	ancestors, _ := out["ancestors"].([]any)

	// @constraint: the walk includes the parent (level 0) plus both sub-claims (level 1).
	gotClaims := map[string]bool{}
	for _, a := range ancestors {
		item := a.(map[string]any)
		rec, _ := item["record"].(map[string]any)
		if ch, ok := rec["claim_handle_id"].(string); ok {
			gotClaims[ch] = true
		}
	}
	require.True(t, gotClaims[parentClaim.String()], "parent claim_terminal present: %v", gotClaims)
	require.True(t, gotClaims[subA.String()], "sub-claim A reached via sub_claim_handle_ids: %v", gotClaims)
	require.True(t, gotClaims[subB.String()], "sub-claim B reached via sub_claim_handle_ids: %v", gotClaims)
}

// TestLineageEndpoints_BySourceReverseLookup seeds leaf_run rows whose
// substitution_refs cite a given (source_kind, source_version_or_id), plus
// a decoy row citing a different source, and asserts GET
// /lineage/by-source/{kind}/{id} returns exactly the matching records. This
// reverse lookup fails silently if the JSONB scan + Go filter is subtly
// wrong (e.g. matches on kind OR id instead of kind AND id), so the decoy
// row is load-bearing.
func TestLineageEndpoints_BySourceReverseLookup(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID, frameID := seedLineageInstance(t, h, "lin-ep-bysrc")
	base := time.Now().UTC()

	wantRunA := uuid.New()
	wantRunB := uuid.New()
	decoyRun := uuid.New()
	srcID := uuid.NewString()

	// @constraint: two rows cite (run, srcID); the decoy cites a different id under the
	// same kind, and another decoy cites the same id under a different kind.
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindLeafRun,
		map[string]any{
			"run_id":            wantRunA.String(),
			"substitution_refs": []map[string]any{{"source_kind": "run", "source_version_or_id": srcID}},
		}, base, "")
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindLeafRun,
		map[string]any{
			"run_id":            wantRunB.String(),
			"substitution_refs": []map[string]any{{"source_kind": "run", "source_version_or_id": srcID}},
		}, base.Add(time.Second), "")
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindLeafRun,
		map[string]any{
			"run_id":            decoyRun.String(),
			"substitution_refs": []map[string]any{{"source_kind": "run", "source_version_or_id": uuid.NewString()}},
		}, base.Add(2*time.Second), "")
	// @constraint: same id, different kind — must NOT match (kind AND id).
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindLeafRun,
		map[string]any{
			"run_id":            uuid.NewString(),
			"substitution_refs": []map[string]any{{"source_kind": "attribute", "source_version_or_id": srcID}},
		}, base.Add(3*time.Second), "")

	status, out := h.httpJSON(t, "GET", "/v1/lineage/by-source/run/"+srcID, nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ := out["records"].([]any)
	require.Len(t, records, 2, "exactly the two (run, srcID)-citing rows must match — not the decoys")

	gotRuns := map[string]bool{}
	for _, r := range records {
		item := r.(map[string]any)
		rec, _ := item["record"].(map[string]any)
		if rid, ok := rec["run_id"].(string); ok {
			gotRuns[rid] = true
		}
	}
	require.True(t, gotRuns[wantRunA.String()])
	require.True(t, gotRuns[wantRunB.String()])
	require.False(t, gotRuns[decoyRun.String()], "decoy (different source id) must not match")

	// @constraint: A source with no citing rows → empty record set, not 404.
	status, out = h.httpJSON(t, "GET", "/v1/lineage/by-source/run/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ = out["records"].([]any)
	require.Empty(t, records)
}

// TestLineageEndpoints_ByProducerReverseLookup seeds claim_terminal rows
// emitted by a given producer (with and without a version_id) plus a decoy
// from a different producer, and asserts GET
// /lineage/by-producer/{name}[?version=] filters correctly.
func TestLineageEndpoints_ByProducerReverseLookup(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID, frameID := seedLineageInstance(t, h, "lin-ep-byprod")
	base := time.Now().UTC()

	// @constraint: two rows from producer "alpha-store" (versions v1, v2) + a decoy from
	// "beta-store".
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": uuid.NewString(), "producer_name": "alpha-store", "version_id": "v1"}, base, persistence.LineageOutcomeCommitted)
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": uuid.NewString(), "producer_name": "alpha-store", "version_id": "v2"}, base.Add(time.Second), persistence.LineageOutcomeCommitted)
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": uuid.NewString(), "producer_name": "beta-store", "version_id": "v1"}, base.Add(2*time.Second), persistence.LineageOutcomeCommitted)

	// @constraint: all alpha-store rows (no version filter).
	status, out := h.httpJSON(t, "GET", "/v1/lineage/by-producer/alpha-store", nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ := out["records"].([]any)
	require.Len(t, records, 2, "both alpha-store rows must match; beta-store must not")
	for _, r := range records {
		item := r.(map[string]any)
		rec, _ := item["record"].(map[string]any)
		require.Equal(t, "alpha-store", rec["producer_name"])
	}

	// @constraint: narrowed by version=v2 → exactly one.
	status, out = h.httpJSON(t, "GET", "/v1/lineage/by-producer/alpha-store?version=v2", nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ = out["records"].([]any)
	require.Len(t, records, 1, "version filter narrows to the single matching row")
	item := records[0].(map[string]any)
	rec, _ := item["record"].(map[string]any)
	require.Equal(t, "v2", rec["version_id"])

	// @constraint: unknown producer → empty set, not error.
	status, out = h.httpJSON(t, "GET", "/v1/lineage/by-producer/ghost-store", nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ = out["records"].([]any)
	require.Empty(t, records)
}
