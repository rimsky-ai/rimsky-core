// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type failingEventTable struct {
	persistence.EventTable
	err error
}

func (f failingEventTable) List(ctx context.Context, filter persistence.EventListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.EventListResult, error) {
	return persistence.EventListResult{}, f.err
}

type eventsFailingTables struct {
	persistence.Tables
	err error
}

func (t eventsFailingTables) Events() persistence.EventTable {
	return failingEventTable{EventTable: t.Tables.Events(), err: t.err}
}

func TestHandler_GetNode_PropagatesReadFailure(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()

	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("worker"))

	injected := errors.New("injected events read failure")
	deps := observability.Deps{
		Tables:    eventsFailingTables{Tables: store, err: injected},
		Queue:     d.Queue(),
		Discovery: observability.NewDiscovery(&nopProber{}),
	}
	r := newRouter(t, deps)

	req := httptest.NewRequest("GET", "/v1/observability/nodes/"+fix.InstanceID.String()+"/worker", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("GET node with a failing Events().List() call: got status %d, want 500 (the read failure must propagate, not be discarded as empty data)", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("error response must carry an error object, got: %v", body)
	}
	if msg, _ := errObj["message"].(string); msg == "" {
		t.Fatalf("error message must be present, got: %v", errObj)
	}
}
