// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unit coverage for the POST /v1/runs/{run_id}/keepalive handler.
//
// The handler's URL-parse + cancel_token auth code paths are exercised
// without touching persistence (the stubs are unreachable on those
// branches). The success (204) and unknown-run (404) cases drive a
// stub Queue + Tables pair so the handler's mapping of
// code:BumpLastProgressAt's (found, error) return into HTTP status
// codes is covered without seeding a full dispatch fixture. End-to-end
// proof that the bump actually advances col:rimsky_node_runs.last_progress_at
// against real backends lives in
// code:persistence/conformance/park_resume.go::testBumpLastProgressAt
// (postgres + sqlite).

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

// keepaliveStubTables stubs persistence.Tables with just enough surface
// for the keepalive handler: a Transaction that invokes the closure with
// a nil tx. Every other Tables method is inherited from the embedded
// interface and panics if reached.
type keepaliveStubTables struct {
	persistence.Tables
}

func (keepaliveStubTables) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, nil)
}

// keepaliveStubQueue stubs persistence.Queue with a fake
// BumpLastProgressAt that returns the configured (found, err) tuple
// and records every invocation.
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

// newKeepaliveRouter builds a chi router with just the keepalive route
// mounted, mirroring the live Start() wiring so URL-param extraction
// (`chi.URLParam(r, "run_id")`) works the same.
func newKeepaliveRouter(c *CallbackServer) http.Handler {
	r := chi.NewRouter()
	r.Post("/v1/runs/{run_id}/keepalive", c.handleKeepalive)
	return r
}

// TestKeepalive_InvalidRunID covers the malformed-URL path: a non-UUID
// run_id segment returns 400 without touching auth or persistence.
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

// TestKeepalive_MissingAuth covers the absent-Authorization path: 401
// before any persistence work.
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

// TestKeepalive_BadTokenShape covers a token missing the ':' separator.
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

// TestKeepalive_SupervisorMismatch covers a well-formed token whose
// supervisor segment doesn't match this server's SupervisorID.
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

// TestKeepalive_RunIDMismatch covers a well-formed token whose dispatch
// segment differs from the URL's run_id.
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

// TestKeepalive_UnknownRun drives a stub Queue that returns found=false
// (modeling the case where the dispatch row has already been reaped) and
// asserts the handler returns 404 with the documented body.
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

// TestKeepalive_Success drives a stub Queue that returns found=true and
// asserts the handler returns 204 with the bump call landing on the
// expected runID.
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
