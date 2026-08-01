// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability_test

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type txCountingTables struct {
	persistence.Tables
	count int32
}

func (t *txCountingTables) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	atomic.AddInt32(&t.count, 1)
	return t.Tables.Transaction(ctx, fn)
}

func TestObservabilityHandlers_ReadEachThroughATransaction(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()

	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("worker"))
	frameID, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "fixture/tx-wrap")
	runID := seedPendingRun(t, ctx, d, fix.NodeID, frameID, fix.MainRunScopeID)

	producer := "tx-wrap-fixture-store"
	claimHandleID := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     []byte(`"/tx-wrap"`),
			HolderSupervisorID: "sup-tx-wrap",
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(time.Hour),
		}, tx); err != nil {
			return err
		}
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:              shared.UUID(uuid.New()),
			ClaimHandleID:   claimHandleID,
			HolderNodeRunID: runID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim handle: %v", err)
	}
	seedEventAt(t, ctx, store, events.SignalKind("terminal/success"), time.Now().UTC())

	counting := &txCountingTables{Tables: store}
	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{
		Tables:         counting,
		Queue:          d.Queue(),
		Driver:         d,
		Discovery:      disc,
		ClaimProducers: []observability.PeerSpec{{Name: producer, Endpoint: "store:9000"}},
		Executors:      []observability.PeerSpec{{Name: "worker", Endpoint: "exec:9000"}},
	}
	r := newRouter(t, deps)

	paths := []string{
		"/v1/observability/claim-producers/" + producer,
		"/v1/observability/templates/" + fix.TemplateHash,
		"/v1/observability/frames/" + frameID.String(),
		"/v1/observability/node-runs/" + runID.String(),
		"/v1/observability/claim-handles/" + claimHandleID.String(),
		"/v1/observability/events",
		"/v1/observability/system/health",
		"/v1/observability/system/summary",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			atomic.StoreInt32(&counting.count, 0)
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if got := atomic.LoadInt32(&counting.count); got < 1 {
				t.Fatalf("GET %s (status=%d) never called Tables.Transaction: got %d calls, want >= 1 — "+
					"the handler must read through inTx rather than issuing untransacted reads", path, w.Code, got)
			}
		})
	}

	singleTxPaths := []string{
		"/v1/observability/instances/" + fix.InstanceID.String(),
		"/v1/observability/nodes/" + fix.InstanceID.String() + "/worker",
	}
	for _, path := range singleTxPaths {
		t.Run(path, func(t *testing.T) {
			atomic.StoreInt32(&counting.count, 0)
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if got := atomic.LoadInt32(&counting.count); got != 1 {
				t.Fatalf("GET %s (status=%d) called Tables.Transaction %d times, want exactly 1 — "+
					"a multi-transaction read risks a torn read across the transactions", path, w.Code, got)
			}
		})
	}
}
