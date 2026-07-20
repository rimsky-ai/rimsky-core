// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestHandler_ListLockHolders_FiltersByProducerName(t *testing.T) {
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

	req := httptest.NewRequest("GET", "/v1/observability/lock-holders?producer_name=topics-ring", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		LockHolders []map[string]any `json:"lock_holders"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.LockHolders) != 1 {
		t.Fatalf("producer_name filter returned %d holders, want 1: %+v", len(body.LockHolders), body.LockHolders)
	}
	if body.LockHolders[0]["id"] != wantID.String() {
		t.Fatalf("id = %v, want %s", body.LockHolders[0]["id"], wantID.String())
	}
}

func TestHandler_ListLockHolders_FiltersByInstanceID(t *testing.T) {
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

	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/observability/lock-holders?instance_id=%s", fixA.InstanceID.String()), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		LockHolders []map[string]any `json:"lock_holders"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.LockHolders) != 1 {
		t.Fatalf("instance_id filter returned %d holders, want 1: %+v", len(body.LockHolders), body.LockHolders)
	}
	if body.LockHolders[0]["id"] != wantID.String() {
		t.Fatalf("id = %v, want %s", body.LockHolders[0]["id"], wantID.String())
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

	req := httptest.NewRequest("GET", "/v1/observability/lock-holders/"+claimID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		LockHolder map[string]any `json:"lock_holder"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.LockHolder["id"] != claimID.String() {
		t.Fatalf("existence answer: id = %v, want %s (the row must answer 'did this claim exist' from itself)",
			body.LockHolder["id"], claimID.String())
	}
	if body.LockHolder["state"] != "committed" {
		t.Fatalf("resolution answer: state = %v, want %q (the row must answer 'how did this claim resolve' from itself, no lineage join)",
			body.LockHolder["state"], "committed")
	}
	if body.LockHolder["resolved_at"] == nil || body.LockHolder["resolved_at"] == "" {
		t.Fatalf("resolution answer: resolved_at = %v, want a non-empty timestamp sourced from the row itself", body.LockHolder["resolved_at"])
	}
}

func TestHandler_ListLockHolders_InvalidInstanceID(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/lock-holders?instance_id=not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
