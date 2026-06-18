// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type keepaliveStubTables struct {
	persistence.Tables
}

func (keepaliveStubTables) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, nil)
}

type keepaliveStubQueue struct {
	persistence.Queue
	found bool
	err   error
	calls []shared.UUID
}

func (q *keepaliveStubQueue) BumpLastProgressAt(_ context.Context, _ persistence.Tx, runID shared.UUID, _ time.Time) (bool, error) {
	q.calls = append(q.calls, runID)
	return q.found, q.err
}

func newKeepaliveRouter(c *CallbackServer) http.Handler {
	r := chi.NewRouter()
	r.Post("/v1/runs/{run_id}/keepalive", c.handleKeepalive)
	return r
}

func TestKeepalive_InvalidRunID(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{Logger: shared.SilentLogger{}, SupervisorID: "sup-1"}
	router := newKeepaliveRouter(c)

	req := httptest.NewRequest(http.MethodPost, "/v1/runs/not-a-uuid/keepalive", nil)
	req.Header.Set("Authorization", "sup-1:"+uuid.NewString())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestKeepalive_MissingAuth(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{Logger: shared.SilentLogger{}, SupervisorID: "sup-1"}
	router := newKeepaliveRouter(c)

	runID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+"/keepalive", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestKeepalive_BadTokenShape(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{Logger: shared.SilentLogger{}, SupervisorID: "sup-1"}
	router := newKeepaliveRouter(c)

	runID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+"/keepalive", nil)
	req.Header.Set("Authorization", "no-colon-here")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestKeepalive_SupervisorMismatch(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{Logger: shared.SilentLogger{}, SupervisorID: "sup-A"}
	router := newKeepaliveRouter(c)

	runID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+"/keepalive", nil)
	req.Header.Set("Authorization", "sup-B:"+runID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestKeepalive_RunIDMismatch(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{Logger: shared.SilentLogger{}, SupervisorID: "sup-1"}
	router := newKeepaliveRouter(c)

	urlRun := uuid.NewString()
	tokenRun := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+urlRun+"/keepalive", nil)
	req.Header.Set("Authorization", "sup-1:"+tokenRun)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestKeepalive_UnknownRun(t *testing.T) {
	t.Parallel()
	queue := &keepaliveStubQueue{found: false}
	c := &CallbackServer{
		Logger:       shared.SilentLogger{},
		SupervisorID: "sup-1",
		Persist:      keepaliveStubTables{},
		Queue:        queue,
	}
	router := newKeepaliveRouter(c)

	runID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+"/keepalive", nil)
	req.Header.Set("Authorization", "sup-1:"+runID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown_run_id") {
		t.Fatalf("body = %q, want unknown_run_id", rec.Body.String())
	}
	if len(queue.calls) != 1 {
		t.Fatalf("BumpLastProgressAt calls = %d, want 1", len(queue.calls))
	}
}

func TestKeepalive_Success(t *testing.T) {
	t.Parallel()
	queue := &keepaliveStubQueue{found: true}
	c := &CallbackServer{
		Logger:       shared.SilentLogger{},
		SupervisorID: "sup-1",
		Persist:      keepaliveStubTables{},
		Queue:        queue,
	}
	router := newKeepaliveRouter(c)

	runID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID.String()+"/keepalive", nil)
	req.Header.Set("Authorization", "sup-1:"+runID.String())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(queue.calls) != 1 {
		t.Fatalf("BumpLastProgressAt calls = %d, want 1", len(queue.calls))
	}
	if queue.calls[0] != shared.UUID(runID) {
		t.Fatalf("BumpLastProgressAt runID = %s, want %s", queue.calls[0], runID)
	}
}
