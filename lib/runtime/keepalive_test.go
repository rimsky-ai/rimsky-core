// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
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

func keepaliveRequest(runID string, token string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID+"/keepalive", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func cancelTokenFor(supervisorID string, runID uuid.UUID) string {
	return supervisorID + ":" + runID.String()
}

func TestKeepalive_InvalidRunID(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{Logger: shared.SilentLogger{}, SupervisorID: "sup-1"}
	router := newKeepaliveRouter(c)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, keepaliveRequest("not-a-uuid", "sup-1:not-a-uuid"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestKeepalive_MissingCancelTokenRejected(t *testing.T) {
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
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, keepaliveRequest(runID.String(), ""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (keepalive requires the cancel_token); body=%s", rec.Code, rec.Body.String())
	}
	if len(queue.calls) != 0 {
		t.Fatalf("BumpLastProgressAt calls = %d, want 0 (no bump without auth)", len(queue.calls))
	}
}

func TestKeepalive_WrongCancelTokenRejected(t *testing.T) {
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
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, keepaliveRequest(runID.String(), cancelTokenFor("sup-1", uuid.New())))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (cancel_token bound to a different run); body=%s", rec.Code, rec.Body.String())
	}
	if len(queue.calls) != 0 {
		t.Fatalf("BumpLastProgressAt calls = %d, want 0 (no bump without auth)", len(queue.calls))
	}
}

func TestKeepalive_WrongSupervisorTokenRejected(t *testing.T) {
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
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, keepaliveRequest(runID.String(), cancelTokenFor("sup-other", runID)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (cancel_token minted by a different supervisor); body=%s", rec.Code, rec.Body.String())
	}
}

func TestKeepalive_MTLSRejectsMissingClientCert(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{
		Logger:       shared.SilentLogger{},
		SupervisorID: "sup-1",
		PeerAuth:     peer.PeerAuthMTLS,
	}
	router := newKeepaliveRouter(c)

	runID := uuid.New()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, keepaliveRequest(runID.String(), cancelTokenFor("sup-1", runID)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (mtls without client cert)", rec.Code)
	}
}

func TestKeepalive_MTLSAcceptsVerifiedPrincipal(t *testing.T) {
	t.Parallel()
	queue := &keepaliveStubQueue{found: true}
	c := &CallbackServer{
		Logger:       shared.SilentLogger{},
		SupervisorID: "sup-1",
		Persist:      keepaliveStubTables{},
		Queue:        queue,
		PeerAuth:     peer.PeerAuthMTLS,
	}
	router := newKeepaliveRouter(c)

	runID := uuid.New()
	req := keepaliveRequest(runID.String(), cancelTokenFor("sup-1", runID))
	leaf := leafForPrincipal(t, "executor-7")
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf},
		VerifiedChains:   [][]*x509.Certificate{{leaf}},
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

func TestKeepalive_MTLSRejectsCertWithoutPrincipal(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{
		Logger:       shared.SilentLogger{},
		SupervisorID: "sup-1",
		PeerAuth:     peer.PeerAuthMTLS,
	}
	router := newKeepaliveRouter(c)

	runID := uuid.New()
	req := keepaliveRequest(runID.String(), cancelTokenFor("sup-1", runID))
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{{}},
		VerifiedChains:   [][]*x509.Certificate{{{}}},
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (cert carries no spiffe principal)", rec.Code)
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

	runID := uuid.New()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, keepaliveRequest(runID.String(), cancelTokenFor("sup-1", runID)))

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
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, keepaliveRequest(runID.String(), cancelTokenFor("sup-1", runID)))

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

func leafForPrincipal(t *testing.T, principal string) *x509.Certificate {
	t.Helper()
	ca, err := pki.GenerateCA(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	issued, err := ca.IssueLeaf(principal, time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), pki.LeafTTL)
	if err != nil {
		t.Fatalf("IssueLeaf: %v", err)
	}
	keyPair, err := tls.X509KeyPair(issued.CertPEM, issued.KeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return leaf
}
