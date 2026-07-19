// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestLineageRunDescendants_HandlerWalksChain(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

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

	var frameID uuid.UUID
	rootScope := mainRunScopeIDForInstance(t, h, shared.UUID(instUUID))
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instUUID), tx)
		if err != nil || len(nodes) == 0 {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := h.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: shared.UUID(instUUID),
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		_ = nodes
		fid, err := h.persist.Frames().InsertRunningFrame(ctx, shared.UUID(instUUID), msgID, rootScope, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	require.NotEqual(t, uuid.Nil, frameID)

	rootRunID := uuid.New()
	child1RunID := uuid.New()
	grandchild1RunID := uuid.New()

	insertLeafRun := func(t *testing.T, runID, parentNodeRunID uuid.UUID, observedAt time.Time) {
		t.Helper()
		rec := map[string]any{
			"run_id":               runID.String(),
			"frame_id":             frameID.String(),
			"state":                "fresh",
			"settling_signal_type": "terminal/success",
		}
		if parentNodeRunID != uuid.Nil {
			rec["parent_run_id"] = parentNodeRunID.String()
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

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/descendants?depth=2", rootRunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, 2, out["depth"])
	descendants, _ := out["descendants"].([]any)
	require.Len(t, descendants, 2, "expected child1 + grandchild1 in the descendant set")

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

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/descendants?depth=1", rootRunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	descendants, _ = out["descendants"].([]any)
	require.Len(t, descendants, 1, "depth=1 should only return child1")
}

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
	rootScope := mainRunScopeIDForInstance(t, h, shared.UUID(instUUID))
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instUUID), tx)
		if err != nil || len(nodes) == 0 {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := h.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{ID: msgID, InstanceID: shared.UUID(instUUID), Type: "test/seed", Sender: "test", SenderKind: "operator"}); err != nil {
			return err
		}
		_ = nodes
		fid, err := h.persist.Frames().InsertRunningFrame(ctx, shared.UUID(instUUID), msgID, rootScope, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	require.NotEqual(t, uuid.Nil, frameID)

	rootRunID := uuid.New()
	child1RunID := uuid.New()
	grandchild1RunID := uuid.New()

	insertLeafRun := func(t *testing.T, runID uuid.UUID, parentNodeRunID uuid.UUID, observedAt time.Time) {
		t.Helper()
		rec := map[string]any{
			"run_id":               runID.String(),
			"frame_id":             frameID.String(),
			"state":                "fresh",
			"settling_signal_type": "terminal/success",
		}
		if parentNodeRunID != uuid.Nil {
			rec["substitution_refs"] = []map[string]any{
				{
					"source_kind":          "run",
					"source_version_or_id": parentNodeRunID.String(),
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

	status, out = h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/ancestors?depth=1", grandchild1RunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	ancestors, _ = out["ancestors"].([]any)
	require.Len(t, ancestors, 1, "depth=1 should only return child1")
}

func TestLineagePrune_DryRunCountMatchesLiveDelete(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

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
	rootScope := mainRunScopeIDForInstance(t, h, shared.UUID(instUUID))
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instUUID), tx)
		if err != nil || len(nodes) == 0 {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := h.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{ID: msgID, InstanceID: shared.UUID(instUUID), Type: "test/seed", Sender: "test", SenderKind: "operator"}); err != nil {
			return err
		}
		_ = nodes
		fid, err := h.persist.Frames().InsertRunningFrame(ctx, shared.UUID(instUUID), msgID, rootScope, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	require.NotEqual(t, uuid.Nil, frameID)

	insertLeafRun := func(t *testing.T, observedAt time.Time) {
		t.Helper()
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

	require.Equal(t, dryBody.WouldHavePruned.Count, liveBody.Deleted,
		"dry-run preview must equal the live delete count")
}

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

	rootScope := mainRunScopeIDForInstance(t, h, shared.UUID(instID))
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instID), tx)
		if err != nil || len(nodes) == 0 {
			return err
		}
		_ = nodes
		msgID := shared.UUID(uuid.New())
		if err := h.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: shared.UUID(instID),
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := h.persist.Frames().InsertRunningFrame(ctx, shared.UUID(instID), msgID, rootScope, tx)
		if err != nil {
			return err
		}
		frameID = uuid.UUID(fid)
		return nil
	}))
	require.NotEqual(t, uuid.Nil, frameID)
	return instID, frameID
}

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

	status, _ = h.httpJSON(t, "GET", "/v1/lineage/runs/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNotFound, status)

	status, _ = h.httpJSON(t, "GET", "/v1/lineage/runs/not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

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

func TestLineageEndpoints_ClaimAncestorsWalksSubClaimChain(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID, frameID := seedLineageInstance(t, h, "lin-ep-claimanc")
	parentClaim := uuid.New()
	subA := uuid.New()
	subB := uuid.New()
	base := time.Now().UTC()

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

	status, out = h.httpJSON(t, "GET", "/v1/lineage/by-source/run/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ = out["records"].([]any)
	require.Empty(t, records)
}

func TestLineageEndpoints_ByProducerReverseLookup(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID, frameID := seedLineageInstance(t, h, "lin-ep-byprod")
	base := time.Now().UTC()

	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": uuid.NewString(), "producer_name": "alpha-store", "version_id": "v1"}, base, persistence.LineageOutcomeCommitted)
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": uuid.NewString(), "producer_name": "alpha-store", "version_id": "v2"}, base.Add(time.Second), persistence.LineageOutcomeCommitted)
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindClaimTerminal,
		map[string]any{"claim_handle_id": uuid.NewString(), "producer_name": "beta-store", "version_id": "v1"}, base.Add(2*time.Second), persistence.LineageOutcomeCommitted)

	status, out := h.httpJSON(t, "GET", "/v1/lineage/by-producer/alpha-store", nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ := out["records"].([]any)
	require.Len(t, records, 2, "both alpha-store rows must match; beta-store must not")
	for _, r := range records {
		item := r.(map[string]any)
		rec, _ := item["record"].(map[string]any)
		require.Equal(t, "alpha-store", rec["producer_name"])
	}

	status, out = h.httpJSON(t, "GET", "/v1/lineage/by-producer/alpha-store?version=v2", nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ = out["records"].([]any)
	require.Len(t, records, 1, "version filter narrows to the single matching row")
	item := records[0].(map[string]any)
	rec, _ := item["record"].(map[string]any)
	require.Equal(t, "v2", rec["version_id"])

	status, out = h.httpJSON(t, "GET", "/v1/lineage/by-producer/ghost-store", nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ = out["records"].([]any)
	require.Empty(t, records)
}

func TestLineageEndpoints_AncestorsDepthCappedAtMax(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/ancestors?depth=999", uuid.NewString()), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, lineageWalkMaxDepth, out["depth"], "depth must clamp to the walk max")
}

func TestLineageEndpoints_AncestorsInvalidDepthFallsBackToDefault(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/ancestors?depth=abc", uuid.NewString()), nil)
	require.Equal(t, http.StatusOK, status, out)
	require.EqualValues(t, lineageWalkDefaultDepth, out["depth"], "unparseable depth must fall back to the default")
}

func TestLineageRunAncestors_NonRunSourceKindRefsExcluded(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)

	instID, frameID := seedLineageInstance(t, h, "lin-anc-nonrun")
	base := time.Now().UTC()

	decoyRunID := uuid.New()
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindLeafRun,
		map[string]any{"run_id": decoyRunID.String(), "state": "fresh"}, base, "")

	leafRunID := uuid.New()
	insertLineageRow(t, h, instID, frameID, persistence.LineageRecordKindLeafRun,
		map[string]any{
			"run_id": leafRunID.String(),
			"state":  "fresh",
			"substitution_refs": []map[string]any{
				{"source_kind": "topics-ring", "source_version_or_id": decoyRunID.String()},
			},
		}, base.Add(time.Second), "")

	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/ancestors?depth=2", leafRunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	ancestors, _ := out["ancestors"].([]any)
	require.Empty(t, ancestors,
		"a substitution_ref whose source_kind is not \"run\" must not be walked as a run ancestor, "+
			"even when its source_version_or_id happens to parse as a UUID matching a real lineage record")
}

func TestLineageEndpoints_BySourceWindowTruncatesAt500(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, frameID := seedLineageInstance(t, h, "lin-bysrc-window")
	base := time.Now().UTC()
	srcID := uuid.NewString()

	targetRec, err := json.Marshal(map[string]any{
		"run_id": uuid.NewString(),
		"substitution_refs": []map[string]any{
			{"source_kind": "run", "source_version_or_id": srcID},
		},
	})
	require.NoError(t, err)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
			ID:         shared.UUID(uuid.New()),
			RecordKind: persistence.LineageRecordKindLeafRun,
			InstanceID: shared.UUID(instID),
			FrameID:    shared.UUID(frameID),
			ObservedAt: base.Add(-1 * time.Hour),
			Record:     targetRec,
		}); err != nil {
			return err
		}
		for i := 0; i < 500; i++ {
			rec, merr := json.Marshal(map[string]any{"run_id": uuid.NewString()})
			if merr != nil {
				return merr
			}
			if err := h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
				ID:         shared.UUID(uuid.New()),
				RecordKind: persistence.LineageRecordKindLeafRun,
				InstanceID: shared.UUID(instID),
				FrameID:    shared.UUID(frameID),
				ObservedAt: base.Add(time.Duration(i) * time.Second),
				Record:     rec,
			}); err != nil {
				return err
			}
		}
		return nil
	}))

	status, out := h.httpJSON(t, "GET", "/v1/lineage/by-source/run/"+srcID, nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ := out["records"].([]any)
	require.Empty(t, records,
		"the by-source scan window is capped at 500 rows ordered by observed_at DESC; "+
			"a match older than the newest 500 rows falls outside the window")
}

func TestLineageEndpoints_ByProducerWindowTruncatesAt500(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, frameID := seedLineageInstance(t, h, "lin-byprod-window")
	base := time.Now().UTC()

	targetRec, err := json.Marshal(map[string]any{
		"claim_handle_id": uuid.NewString(),
		"producer_name":   "window-store",
		"version_id":      "v1",
	})
	require.NoError(t, err)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
			ID:         shared.UUID(uuid.New()),
			RecordKind: persistence.LineageRecordKindClaimTerminal,
			InstanceID: shared.UUID(instID),
			FrameID:    shared.UUID(frameID),
			ObservedAt: base.Add(-1 * time.Hour),
			Record:     targetRec,
			Outcome:    persistence.LineageOutcomeCommitted,
		}); err != nil {
			return err
		}
		for i := 0; i < 500; i++ {
			rec, merr := json.Marshal(map[string]any{
				"claim_handle_id": uuid.NewString(),
				"producer_name":   "filler-store",
				"version_id":      "v1",
			})
			if merr != nil {
				return merr
			}
			if err := h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
				ID:         shared.UUID(uuid.New()),
				RecordKind: persistence.LineageRecordKindClaimTerminal,
				InstanceID: shared.UUID(instID),
				FrameID:    shared.UUID(frameID),
				ObservedAt: base.Add(time.Duration(i) * time.Second),
				Record:     rec,
				Outcome:    persistence.LineageOutcomeCommitted,
			}); err != nil {
				return err
			}
		}
		return nil
	}))

	status, out := h.httpJSON(t, "GET", "/v1/lineage/by-producer/window-store", nil)
	require.Equal(t, http.StatusOK, status, out)
	records, _ := out["records"].([]any)
	require.Empty(t, records,
		"the by-producer scan window is capped at 500 rows ordered by observed_at DESC; "+
			"a match older than the newest 500 rows falls outside the window")
}

func TestLineageRunDescendants_MatchesRuntimeWriterRecordShape(t *testing.T) {
	t.Parallel()
	h, teardown := newHarness(t)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, frameID := seedLineageInstance(t, h, "lin-desc-writer-shape")
	parentRunID := shared.UUID(uuid.New())
	childRunID := shared.UUID(uuid.New())
	base := time.Now().UTC()

	parentRec := runtime.LeafRunRecord{
		NodeRunID:          parentRunID,
		FrameID:            shared.UUID(frameID),
		State:              "fresh",
		SettlingSignalType: "terminal/success",
	}
	childRec := runtime.LeafRunRecord{
		NodeRunID:          childRunID,
		FrameID:            shared.UUID(frameID),
		ParentNodeRunID:    parentRunID.String(),
		State:              "fresh",
		SettlingSignalType: "terminal/success",
	}
	parentBytes, err := json.Marshal(parentRec)
	require.NoError(t, err)
	childBytes, err := json.Marshal(childRec)
	require.NoError(t, err)

	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
			ID:         shared.UUID(uuid.New()),
			RecordKind: persistence.LineageRecordKindLeafRun,
			InstanceID: shared.UUID(instID),
			FrameID:    shared.UUID(frameID),
			ObservedAt: base,
			Record:     parentBytes,
		}); err != nil {
			return err
		}
		return h.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
			ID:         shared.UUID(uuid.New()),
			RecordKind: persistence.LineageRecordKindLeafRun,
			InstanceID: shared.UUID(instID),
			FrameID:    shared.UUID(frameID),
			ObservedAt: base.Add(time.Second),
			Record:     childBytes,
		})
	}))

	status, out := h.httpJSON(t, "GET", fmt.Sprintf("/v1/lineage/runs/%s/descendants?depth=1", parentRunID.String()), nil)
	require.Equal(t, http.StatusOK, status, out)
	descendants, _ := out["descendants"].([]any)
	require.Len(t, descendants, 1,
		"descendants walk must find the child using the runtime writer's own LeafRunRecord.parent_run_id json shape")
	item := descendants[0].(map[string]any)
	rec, _ := item["record"].(map[string]any)
	require.Equal(t, childRunID.String(), rec["run_id"])
}
