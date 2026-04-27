package attributes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/shared"
)

// fakeAttributesStore is a tiny in-memory NodeAttributesStore used by the
// callback handler test. It keeps the test independent of the postgres
// helpers in store.go (those are exercised in store_test.go via
// testcontainers).
type fakeAttributesStore struct {
	mu   sync.Mutex
	rows map[shared.UUID]*Row
}

func newFakeAttributesStore() *fakeAttributesStore {
	return &fakeAttributesStore{rows: map[shared.UUID]*Row{}}
}

func (f *fakeAttributesStore) Get(_ context.Context, nodeID shared.UUID) (*Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[nodeID]
	if !ok {
		return nil, nil
	}
	cp := *r
	cp.Data = cloneMap(r.Data)
	return &cp, nil
}

func (f *fakeAttributesStore) Upsert(_ context.Context, nodeID shared.UUID, runAttempt int, data map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[nodeID] = &Row{NodeID: nodeID, RunAttempt: runAttempt, Data: cloneMap(data)}
	return nil
}

func (f *fakeAttributesStore) MergeDelta(_ context.Context, nodeID shared.UUID, delta map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[nodeID]
	if !ok {
		return errors.New("no row for node")
	}
	for k, v := range delta {
		r.Data[k] = v
	}
	return nil
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// mountHandler constructs a chi router with the §12.5 route and returns a
// configured test server. The router is needed because Handler relies on
// chi.URLParam.
func mountHandler(t *testing.T, deps HandlerDeps) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Method(http.MethodPost, "/v1/attributes/{node_id}", Handler(deps))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func postDelta(t *testing.T, srv *httptest.Server, nodeID shared.UUID, token string, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/attributes/"+nodeID.String(), bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestHandler_HappyPath(t *testing.T) {
	t.Parallel()

	store := newFakeAttributesStore()
	nodeID := uuid.New()
	// Row must exist (the supervisor Upserts at dispatch).
	if err := store.Upsert(context.Background(), nodeID, 0, map[string]any{"area": "northwest"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	auth := func(token string, gotID shared.UUID) error {
		if token != "tok-123" {
			return ErrUnauthorizedCallback
		}
		if gotID != nodeID {
			return ErrUnauthorizedCallback
		}
		return nil
	}
	srv := mountHandler(t, HandlerDeps{Store: store, Auth: auth, Logger: shared.SilentLogger{}})

	resp := postDelta(t, srv, nodeID, "tok-123", map[string]any{
		"delta": map[string]any{"x": 1, "subtopic": "sea-otters"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: want 204 got %d", resp.StatusCode)
	}

	row, err := store.Get(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row == nil {
		t.Fatalf("row missing")
	}
	if row.Data["area"] != "northwest" {
		t.Fatalf("area not preserved: %v", row.Data["area"])
	}
	// json decode lifts numbers to float64; compare as float64.
	if got, ok := row.Data["x"].(float64); !ok || got != 1 {
		t.Fatalf("x not merged: %v (%T)", row.Data["x"], row.Data["x"])
	}
	if row.Data["subtopic"] != "sea-otters" {
		t.Fatalf("subtopic not merged: %v", row.Data["subtopic"])
	}
}

func TestHandler_BearerPrefixAccepted(t *testing.T) {
	t.Parallel()

	store := newFakeAttributesStore()
	nodeID := uuid.New()
	_ = store.Upsert(context.Background(), nodeID, 0, map[string]any{})

	auth := func(token string, _ shared.UUID) error {
		if token != "tok-bearer" {
			return ErrUnauthorizedCallback
		}
		return nil
	}
	srv := mountHandler(t, HandlerDeps{Store: store, Auth: auth})

	resp := postDelta(t, srv, nodeID, "Bearer tok-bearer", map[string]any{"delta": map[string]any{}})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: want 204 got %d", resp.StatusCode)
	}
}

func TestHandler_MissingAuth(t *testing.T) {
	t.Parallel()

	store := newFakeAttributesStore()
	srv := mountHandler(t, HandlerDeps{Store: store, Auth: func(string, shared.UUID) error { return nil }})

	resp := postDelta(t, srv, uuid.New(), "", map[string]any{"delta": map[string]any{}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: want 401 got %d", resp.StatusCode)
	}
}

func TestHandler_BadAuth(t *testing.T) {
	t.Parallel()

	store := newFakeAttributesStore()
	auth := func(string, shared.UUID) error { return ErrUnauthorizedCallback }
	srv := mountHandler(t, HandlerDeps{Store: store, Auth: auth})

	resp := postDelta(t, srv, uuid.New(), "wrong-tok", map[string]any{"delta": map[string]any{}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: want 401 got %d", resp.StatusCode)
	}
}

func TestHandler_BadNodeID(t *testing.T) {
	t.Parallel()

	store := newFakeAttributesStore()
	srv := mountHandler(t, HandlerDeps{Store: store, Auth: func(string, shared.UUID) error { return nil }})

	resp, err := http.Post(srv.URL+"/v1/attributes/not-a-uuid", "application/json",
		strings.NewReader(`{"delta":{}}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: want 400 got %d", resp.StatusCode)
	}
}

func TestHandler_InvalidJSON(t *testing.T) {
	t.Parallel()

	store := newFakeAttributesStore()
	auth := func(string, shared.UUID) error { return nil }
	srv := mountHandler(t, HandlerDeps{Store: store, Auth: auth})

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/attributes/"+uuid.New().String(),
		strings.NewReader("not-json{"))
	req.Header.Set("Authorization", "tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: want 400 got %d", resp.StatusCode)
	}
}

func TestHandler_MergeFailureReturns500(t *testing.T) {
	t.Parallel()

	// Fake store with no row → MergeDelta errors → handler returns 500.
	store := newFakeAttributesStore()
	auth := func(string, shared.UUID) error { return nil }
	srv := mountHandler(t, HandlerDeps{Store: store, Auth: auth})

	resp := postDelta(t, srv, uuid.New(), "tok", map[string]any{"delta": map[string]any{"x": 1}})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status: want 500 got %d", resp.StatusCode)
	}
}
