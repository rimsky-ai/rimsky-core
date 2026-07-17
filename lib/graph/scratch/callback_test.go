// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scratch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type fakeScratchWriter struct {
	mu      sync.Mutex
	writes  map[shared.UUID][]byte
	failErr error
}

func newFakeScratchWriter() *fakeScratchWriter {
	return &fakeScratchWriter{writes: map[shared.UUID][]byte{}}
}

func (f *fakeScratchWriter) Write(_ context.Context, runID shared.UUID, b []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	f.writes[runID] = cp
	return nil
}

func (f *fakeScratchWriter) get(runID shared.UUID) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[runID]
}

func mountHandler(t *testing.T, deps HandlerDeps) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Method(http.MethodPost, "/v1/runs/{run_id}/scratch", Handler(deps))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func postScratch(t *testing.T, srv *httptest.Server, runID shared.UUID, token string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/runs/"+runID.String()+"/scratch", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestHandler_HappyPath(t *testing.T) {
	t.Parallel()

	writer := newFakeScratchWriter()
	runID := uuid.New()

	auth := func(r *http.Request, gotID shared.UUID) error {
		if r.Header.Get("Authorization") != "tok-123" {
			return ErrUnauthorizedCallback
		}
		if gotID != runID {
			return ErrUnauthorizedCallback
		}
		return nil
	}
	srv := mountHandler(t, HandlerDeps{Writer: writer, Auth: auth, Logger: shared.SilentLogger{}})

	want := []byte{0x00, 0x01, 0x02, 0xFF, 'a', 'b'}
	resp := postScratch(t, srv, runID, "tok-123", want)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: want 204 got %d", resp.StatusCode)
	}

	got := writer.get(runID)
	if !bytes.Equal(got, want) {
		t.Fatalf("scratch bytes mismatch: want %x got %x", want, got)
	}
}

func TestHandler_BearerPrefixAccepted(t *testing.T) {
	t.Parallel()

	writer := newFakeScratchWriter()
	runID := uuid.New()
	auth := func(r *http.Request, _ shared.UUID) error {
		if r.Header.Get("Authorization") != "Bearer tok-bearer" {
			return ErrUnauthorizedCallback
		}
		return nil
	}
	srv := mountHandler(t, HandlerDeps{Writer: writer, Auth: auth})

	resp := postScratch(t, srv, runID, "Bearer tok-bearer", []byte("abc"))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: want 204 got %d", resp.StatusCode)
	}
}

func TestHandler_MissingAuth(t *testing.T) {
	t.Parallel()

	writer := newFakeScratchWriter()
	auth := func(r *http.Request, _ shared.UUID) error {
		if r.Header.Get("Authorization") == "" {
			return ErrUnauthorizedCallback
		}
		return nil
	}
	srv := mountHandler(t, HandlerDeps{Writer: writer, Auth: auth})

	resp := postScratch(t, srv, uuid.New(), "", []byte("x"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: want 401 got %d", resp.StatusCode)
	}
}

func TestHandler_BadAuth(t *testing.T) {
	t.Parallel()

	writer := newFakeScratchWriter()
	auth := func(*http.Request, shared.UUID) error { return ErrUnauthorizedCallback }
	srv := mountHandler(t, HandlerDeps{Writer: writer, Auth: auth})

	resp := postScratch(t, srv, uuid.New(), "wrong-tok", []byte("x"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: want 401 got %d", resp.StatusCode)
	}
}

func TestHandler_BadRunID(t *testing.T) {
	t.Parallel()

	writer := newFakeScratchWriter()
	srv := mountHandler(t, HandlerDeps{Writer: writer, Auth: func(*http.Request, shared.UUID) error { return nil }})

	resp, err := http.Post(srv.URL+"/v1/runs/not-a-uuid/scratch", "application/octet-stream",
		strings.NewReader("x"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: want 400 got %d", resp.StatusCode)
	}
}

func TestHandler_BodyTooLarge(t *testing.T) {
	t.Parallel()

	writer := newFakeScratchWriter()
	srv := mountHandler(t, HandlerDeps{Writer: writer, Auth: func(*http.Request, shared.UUID) error { return nil }})

	const maxBody = 64 * 1024 * 1024
	body := io.MultiReader(
		io.LimitReader(zeroReader{}, maxBody),
		strings.NewReader("X"),
	)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/runs/"+uuid.New().String()+"/scratch", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "tok")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: want 413 got %d", resp.StatusCode)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestHandler_WriteFailureReturns500(t *testing.T) {
	t.Parallel()

	writer := newFakeScratchWriter()
	writer.failErr = errors.New("synthetic write failure")
	auth := func(*http.Request, shared.UUID) error { return nil }
	srv := mountHandler(t, HandlerDeps{Writer: writer, Auth: auth})

	resp := postScratch(t, srv, uuid.New(), "tok", []byte("x"))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: want 500 got %d", resp.StatusCode)
	}
}

func TestHandler_RunRowMissingReturns410(t *testing.T) {
	t.Parallel()

	writer := newFakeScratchWriter()
	writer.failErr = ErrRunRowMissing
	auth := func(*http.Request, shared.UUID) error { return nil }
	srv := mountHandler(t, HandlerDeps{Writer: writer, Auth: auth})

	resp := postScratch(t, srv, uuid.New(), "tok", []byte("x"))
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("status: want 410 got %d", resp.StatusCode)
	}
}

func TestHandler_PanicOnNilAuth(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil Auth")
		}
	}()
	_ = Handler(HandlerDeps{Writer: newFakeScratchWriter(), Auth: nil})
}

func TestHandler_PanicOnNilWriter(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil Writer")
		}
	}()
	_ = Handler(HandlerDeps{Writer: nil, Auth: func(*http.Request, shared.UUID) error { return nil }})
}
