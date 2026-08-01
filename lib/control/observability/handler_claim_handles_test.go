// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func seedClaimHandle(t *testing.T, ctx context.Context, store persistence.Tables, producerName string, holderNodeID shared.UUID) shared.UUID {
	t.Helper()
	id := shared.UUID(uuid.New())
	intent := "rw"
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 id,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"scope-1"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-1",
			HolderNodeID:       holderNodeID,
			ExpiresAt:          time.Now().Add(5 * time.Minute),
		}, tx)
	}); err != nil {
		t.Fatalf("seedClaimHandle: %v", err)
	}
	return id
}

func TestHandler_ListClaimHandles_FiltersByProducerName(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fixA := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))
	fixB := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	wantID := seedClaimHandle(t, ctx, store, "topics-ring", fixA.NodeID)
	_ = seedClaimHandle(t, ctx, store, "other-store", fixB.NodeID)

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/claim-handles?producer_name=topics-ring", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		ClaimHandles []map[string]any `json:"claim_handles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.ClaimHandles) != 1 {
		t.Fatalf("producer_name filter returned %d claim handles, want 1: %+v", len(body.ClaimHandles), body.ClaimHandles)
	}
	if body.ClaimHandles[0]["id"] != wantID.String() {
		t.Fatalf("id = %v, want %s", body.ClaimHandles[0]["id"], wantID.String())
	}
}

func TestHandler_ListClaimHandles_FiltersByInstanceID(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fixA := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))
	fixB := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	wantID := seedClaimHandle(t, ctx, store, "topics-ring", fixA.NodeID)
	_ = seedClaimHandle(t, ctx, store, "topics-ring", fixB.NodeID)

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/claim-handles?instance_id=%s", fixA.InstanceID.String()), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		ClaimHandles []map[string]any `json:"claim_handles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.ClaimHandles) != 1 {
		t.Fatalf("instance_id filter returned %d claim handles, want 1: %+v", len(body.ClaimHandles), body.ClaimHandles)
	}
	if body.ClaimHandles[0]["id"] != wantID.String() {
		t.Fatalf("id = %v, want %s", body.ClaimHandles[0]["id"], wantID.String())
	}
}

// @concept: claim-handle
func TestHandler_GetClaimHandle_ForensicAnswerFromRowAlone(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	claimID := seedClaimHandle(t, ctx, store, "topics-ring", fix.NodeID)
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHandles().Promote(ctx, claimID, "sup-1", spec.ClaimHandleStateCommitted, tx)
	}); err != nil {
		t.Fatalf("promote claim handle: %v", err)
	}

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/claim-handles/"+claimID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		ClaimHandle map[string]any `json:"claim_handle"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ClaimHandle["id"] != claimID.String() {
		t.Fatalf("existence answer: id = %v, want %s (the row must answer 'did this claim exist' from itself)",
			body.ClaimHandle["id"], claimID.String())
	}
	if body.ClaimHandle["state"] != "committed" {
		t.Fatalf("resolution answer: state = %v, want %q (the row must answer 'how did this claim resolve' from itself, no lineage join)",
			body.ClaimHandle["state"], "committed")
	}
	if body.ClaimHandle["resolved_at"] == nil || body.ClaimHandle["resolved_at"] == "" {
		t.Fatalf("resolution answer: resolved_at = %v, want a non-empty timestamp sourced from the row itself", body.ClaimHandle["resolved_at"])
	}
}

func TestHandler_ListClaimHandles_InvalidInstanceID(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/claim-handles?instance_id=not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ListClaimHandles_InvalidHolderNodeID(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/claim-handles?holder_node_id=not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandler_ListClaimHandles_FiltersByHolderNodeID(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fixA := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))
	fixB := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	wantID := seedClaimHandle(t, ctx, store, "topics-ring", fixA.NodeID)
	_ = seedClaimHandle(t, ctx, store, "topics-ring", fixB.NodeID)

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/claim-handles?holder_node_id=%s", fixA.NodeID.String()), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		ClaimHandles []map[string]any `json:"claim_handles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.ClaimHandles) != 1 {
		t.Fatalf("holder_node_id filter returned %d claim handles, want 1: %+v", len(body.ClaimHandles), body.ClaimHandles)
	}
	if body.ClaimHandles[0]["id"] != wantID.String() {
		t.Fatalf("id = %v, want %s", body.ClaimHandles[0]["id"], wantID.String())
	}
}

func TestHandler_ListClaimHandles_FiltersByHolderSupervisorID(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))

	insertClaimHandle := func(supervisorID string) shared.UUID {
		id := shared.UUID(uuid.New())
		producer := "topics-ring"
		intent := "rw"
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID:                 id,
				LockKind:           persistence.LockKindScope,
				ProducerName:       &producer,
				ClaimScopeData:     []byte(`"scope-1"`),
				Intent:             &intent,
				HolderSupervisorID: supervisorID,
				HolderNodeID:       fix.NodeID,
				ExpiresAt:          time.Now().Add(5 * time.Minute),
			}, tx)
		}); err != nil {
			t.Fatalf("insertClaimHandle(%s): %v", supervisorID, err)
		}
		return id
	}
	wantID := insertClaimHandle("sup-alpha")
	_ = insertClaimHandle("sup-beta")

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/claim-handles?holder_supervisor_id=sup-alpha", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		ClaimHandles []map[string]any `json:"claim_handles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.ClaimHandles) != 1 {
		t.Fatalf("holder_supervisor_id filter returned %d claim handles, want 1: %+v", len(body.ClaimHandles), body.ClaimHandles)
	}
	if body.ClaimHandles[0]["id"] != wantID.String() {
		t.Fatalf("id = %v, want %s", body.ClaimHandles[0]["id"], wantID.String())
	}
}

func TestHandler_ListClaimHandles_FiltersByNodeType(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fixA := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("alpha-node-type"))
	fixB := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("beta-node-type"))

	wantID := seedClaimHandle(t, ctx, store, "topics-ring", fixA.NodeID)
	_ = seedClaimHandle(t, ctx, store, "topics-ring", fixB.NodeID)

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/claim-handles?node_type=alpha-node-type", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		ClaimHandles []map[string]any `json:"claim_handles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.ClaimHandles) != 1 {
		t.Fatalf("node_type filter returned %d claim handles, want 1: %+v", len(body.ClaimHandles), body.ClaimHandles)
	}
	if body.ClaimHandles[0]["id"] != wantID.String() {
		t.Fatalf("id = %v, want %s", body.ClaimHandles[0]["id"], wantID.String())
	}
}

func TestHandler_GetClaimHandle_ClaimHoldersEnrichmentPopulated(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))
	frameID, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "fixture/claim-holder-enrichment")
	runID := seedPendingRun(t, ctx, d, fix.NodeID, frameID, fix.MainRunScopeID)

	claimID := seedClaimHandle(t, ctx, store, "topics-ring", fix.NodeID)
	holderID := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:              holderID,
			ClaimHandleID:   claimID,
			HolderNodeRunID: runID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim holder: %v", err)
	}

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/claim-handles/"+claimID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		ClaimHolders []map[string]any `json:"claim_holders"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.ClaimHolders) != 1 {
		t.Fatalf("claim_holders: got %d entries, want 1: %+v", len(body.ClaimHolders), body.ClaimHolders)
	}
	if body.ClaimHolders[0]["id"] != holderID.String() {
		t.Fatalf("claim_holders[0].id = %v, want %s", body.ClaimHolders[0]["id"], holderID.String())
	}
	if body.ClaimHolders[0]["holder_run_id"] != runID.String() {
		t.Fatalf("claim_holders[0].holder_run_id = %v, want %s", body.ClaimHolders[0]["holder_run_id"], runID.String())
	}
}
