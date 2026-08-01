// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func bootAdminStore(t *testing.T, table string) *Store {
	t.Helper()
	pool, dsn := bootPostgresTestContainer(t)
	createTestItemsTable(t, pool, table)

	pp := &PickPolicy{
		ItemsTable:        table,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: time.Minute,
	}
	st, err := New(context.Background(), Config{
		Connection:     dsn,
		WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
		PickPolicies:   map[string]*PickPolicy{"@queue": pp},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestAdminInsertItems_201InsertsAvailableRows(t *testing.T) {
	st := bootAdminStore(t, "admin_items_ok")
	handler := st.AdminHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/items/%40queue",
		strings.NewReader(`{"items":[{"payload":{"a":1}},{"payload":{"a":2}}]}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}

	out, err := st.Open(context.Background(), "c1", "@queue", claimproducer.IntentReadWrite)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !out.Available {
		t.Fatalf("Open after admin insert: expected Available, got Unavailable")
	}
}

func TestAdminInsertItems_400EmptyItems(t *testing.T) {
	st := bootAdminStore(t, "admin_items_empty")
	handler := st.AdminHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/items/%40queue", strings.NewReader(`{"items":[]}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminInsertItems_400MissingPayloadField(t *testing.T) {
	st := bootAdminStore(t, "admin_items_missing_payload")
	handler := st.AdminHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/items/%40queue", strings.NewReader(`{"items":[{}]}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an item with no payload field; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminInsertItems_400MalformedJSONBody(t *testing.T) {
	st := bootAdminStore(t, "admin_items_malformed_body")
	handler := st.AdminHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/items/%40queue", strings.NewReader(`{not-json`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a malformed JSON body; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminInsertItems_400UnknownSelector(t *testing.T) {
	st := bootAdminStore(t, "admin_items_unknown_selector")
	handler := st.AdminHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/items/%40nope", strings.NewReader(`{"items":[{"payload":{"a":1}}]}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (InsertItems reports no pick policy for the selector); body=%s", w.Code, w.Body.String())
	}
}

func TestAdminInsertItems_405NonPOST(t *testing.T) {
	st := bootAdminStore(t, "admin_items_method")
	handler := st.AdminHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/items/%40queue", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}
