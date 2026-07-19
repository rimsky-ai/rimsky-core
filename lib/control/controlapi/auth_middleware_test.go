// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type authTestHarness struct {
	state     *AuthState
	tables    persistence.Tables
	plaintext string
}

func newAuthTestHarness(t *testing.T) authTestHarness {
	t.Helper()
	h := newUnseededAuthTestHarness(t)
	ctx := context.Background()
	plaintext, hash, err := auth.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := h.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.tables.APIKeys().Insert(ctx, persistence.APIKey{
			ID:          shared.UUID{9, 9, 9},
			Name:        "seed",
			KeyHash:     hash[:],
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   h.state.Clock.Now(),
		}, tx)
	}); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	h.plaintext = plaintext
	return h
}

func newUnseededAuthTestHarness(t *testing.T) authTestHarness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	clock := shared.NewControllableClock(time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC))
	state := &AuthState{
		Tables:   d.Tables(),
		Registry: BuildV1Registry(),
		Clock:    clock,
		Logger:   shared.SilentLogger{},
	}
	return authTestHarness{state: state, tables: d.Tables()}
}

func lastAttemptedRow(t *testing.T, tables persistence.Tables) map[string]any {
	t.Helper()
	var res persistence.EventListResult
	err := tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var lerr error
		res, lerr = tables.Events().List(ctx, persistence.EventListFilter{
			Kind: auth.EventAccessAttempted,
		}, persistence.ListPagination{Limit: 1}, tx)
		return lerr
	})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(res.Events) == 0 {
		t.Fatalf("no auth.access_attempted row present after request")
	}
	return res.Events[0].Payload
}

func TestGate_DryRunFlagSetsModeAndReadExecutes(t *testing.T) {
	h := newAuthTestHarness(t)

	var observedMode auth.Mode
	probe := func(w http.ResponseWriter, r *http.Request) {
		observedMode = ModeFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Get("/v1/instances", h.state.gateByAction("instance:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/instances?dry_run=true", nil)
	req.Header.Set("Authorization", "Bearer "+h.plaintext)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	if observedMode != auth.ModeDryRun {
		t.Fatalf("ModeFromContext: got %q want %q", observedMode, auth.ModeDryRun)
	}

	payload := lastAttemptedRow(t, h.tables)
	if payload["mode"] != string(auth.ModeDryRun) {
		t.Errorf("audit mode: got %v want %q", payload["mode"], auth.ModeDryRun)
	}
	if executed, _ := payload["executed"].(bool); !executed {
		t.Errorf("audit executed: got %v want true (read runs even under dry_run)", payload["executed"])
	}
}

func TestGate_StreamingHandlerCanFlush(t *testing.T) {
	h := newAuthTestHarness(t)

	probe := func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: hi\n\n")
		flusher.Flush()
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Get("/v1/mcp", h.state.gateByAction("mcp:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+h.plaintext)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200 (capturingWriter masked http.Flusher?); body=%q",
			resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type: got %q want text/event-stream", ct)
	}
	if !strings.Contains(string(body), "data: hi") {
		t.Fatalf("stream body: got %q want the flushed SSE event", body)
	}
}

func TestGate_ExecuteBeatsDryRun_MultiEntryGrant(t *testing.T) {
	for _, tc := range []struct {
		name        string
		permissions string
	}{
		{
			name:        "dry-run-listed-first",
			permissions: `[{"action":"instance:read","mode":"dry_run"},{"action":"instance:read","mode":"execute"}]`,
		},
		{
			name:        "execute-listed-first",
			permissions: `[{"action":"instance:read","mode":"execute"},{"action":"instance:read","mode":"dry_run"}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			d, err := persistence.Open(ctx, persistence.Config{
				Driver: "sqlite",
				SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
			})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			t.Cleanup(func() { _ = d.Close() })
			clock := shared.NewControllableClock(time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC))
			state := &AuthState{
				Tables:   d.Tables(),
				Registry: BuildV1Registry(),
				Clock:    clock,
				Logger:   shared.SilentLogger{},
			}
			plaintext, hash, err := auth.Mint()
			if err != nil {
				t.Fatalf("mint: %v", err)
			}
			if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return d.Tables().APIKeys().Insert(ctx, persistence.APIKey{
					ID:          shared.UUID{1, 2, 3},
					Name:        "multi-entry",
					KeyHash:     hash[:],
					Permissions: []byte(tc.permissions),
					CreatedAt:   clock.Now(),
				}, tx)
			}); err != nil {
				t.Fatalf("seed key: %v", err)
			}

			var observedMode auth.Mode
			probe := func(w http.ResponseWriter, r *http.Request) {
				observedMode = ModeFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}
			r := chi.NewRouter()
			r.Use(state.IdentityResolver())
			r.Get("/v1/instances", state.gateByAction("instance:read", probe))

			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/instances", nil)
			req.Header.Set("Authorization", "Bearer "+plaintext)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d want 200", resp.StatusCode)
			}
			if observedMode != auth.ModeExecute {
				t.Fatalf("ModeFromContext: got %q want %q (execute must beat dry_run regardless of grant-entry order)", observedMode, auth.ModeExecute)
			}
			payload := lastAttemptedRow(t, d.Tables())
			if payload["mode"] != string(auth.ModeExecute) {
				t.Errorf("audit mode: got %v want %q", payload["mode"], auth.ModeExecute)
			}
		})
	}
}

func TestGate_DefaultModeIsExecute(t *testing.T) {
	h := newAuthTestHarness(t)

	var observedMode auth.Mode
	probe := func(w http.ResponseWriter, r *http.Request) {
		observedMode = ModeFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Get("/v1/instances", h.state.gateByAction("instance:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/instances", nil)
	req.Header.Set("Authorization", "Bearer "+h.plaintext)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if observedMode != auth.ModeExecute {
		t.Fatalf("ModeFromContext: got %q want %q", observedMode, auth.ModeExecute)
	}
	payload := lastAttemptedRow(t, h.tables)
	if payload["mode"] != string(auth.ModeExecute) {
		t.Errorf("audit mode: got %v want %q", payload["mode"], auth.ModeExecute)
	}
	if executed, _ := payload["executed"].(bool); !executed {
		t.Errorf("audit executed: got %v want true", payload["executed"])
	}
}

func TestCaptureBody_TruncatesAuditMarkerButKeepsFullHandlerBody(t *testing.T) {
	body := bytes.Repeat([]byte("a"), auditBodyCapBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handlerBody, auditBytes, rejected := captureBody(req, rec, shared.SilentLogger{})
	if rejected {
		t.Fatalf("rejected: got true want false (body is under the handler cap)")
	}
	if !bytes.Equal(handlerBody, body) {
		t.Fatalf("handlerBody: got %d bytes want %d bytes untouched", len(handlerBody), len(body))
	}
	if bytes.Equal(auditBytes, body) {
		t.Fatalf("auditBytes: got the full body; want the truncation marker")
	}
	var marker struct {
		AuditTruncated bool `json:"_audit_truncated"`
		ObservedBytes  int  `json:"_audit_observed_bytes"`
	}
	if err := json.Unmarshal(auditBytes, &marker); err != nil {
		t.Fatalf("auditBytes not valid JSON marker: %v (%s)", err, auditBytes)
	}
	if !marker.AuditTruncated {
		t.Fatalf("marker._audit_truncated: got false want true")
	}
	if marker.ObservedBytes != len(body) {
		t.Fatalf("marker._audit_observed_bytes: got %d want %d", marker.ObservedBytes, len(body))
	}
}

func TestCaptureBody_RejectsBodyOverHandlerMax(t *testing.T) {
	body := bytes.Repeat([]byte("b"), auditBodyHandlerMaxBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handlerBody, auditBytes, rejected := captureBody(req, rec, shared.SilentLogger{})
	if !rejected {
		t.Fatalf("rejected: got false want true (body exceeds handler cap)")
	}
	if handlerBody != nil {
		t.Fatalf("handlerBody: got %d bytes want nil on reject", len(handlerBody))
	}
	if auditBytes != nil {
		t.Fatalf("auditBytes: got %d bytes want nil on reject", len(auditBytes))
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if maxBytes, ok := out["max_bytes"].(float64); !ok || int(maxBytes) != auditBodyHandlerMaxBytes {
		t.Fatalf("response max_bytes: got %v want %d", out["max_bytes"], auditBodyHandlerMaxBytes)
	}
}

func TestGate_OversizedBodyRejectedBeforeHandlerRuns(t *testing.T) {
	h := newAuthTestHarness(t)

	var handlerRan bool
	probe := func(w http.ResponseWriter, r *http.Request) {
		handlerRan = true
		w.WriteHeader(http.StatusOK)
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Post("/v1/probe", h.state.gateByAction("instance:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	oversized := bytes.Repeat([]byte("c"), auditBodyHandlerMaxBytes+1)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/probe", bytes.NewReader(oversized))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.plaintext)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
	if handlerRan {
		t.Fatalf("handler ran despite an oversized body; captureBody's early-return contract is broken")
	}
}

func TestGate_AuditRowTruncatesOversizedRequestParams(t *testing.T) {
	h := newAuthTestHarness(t)

	probe := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Post("/v1/probe", h.state.gateByAction("instance:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	oversized := bytes.Repeat([]byte("d"), auditBodyCapBytes+1)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/probe", bytes.NewReader(oversized))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.plaintext)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200 (body is under the handler cap)", resp.StatusCode)
	}

	payload := lastAttemptedRow(t, h.tables)
	rp, ok := payload["request_params"].(map[string]any)
	if !ok {
		t.Fatalf("request_params: got %T want map (truncation marker)", payload["request_params"])
	}
	if truncated, _ := rp["_audit_truncated"].(bool); !truncated {
		t.Fatalf("request_params._audit_truncated: got %v want true", rp["_audit_truncated"])
	}
	if observed, ok := rp["_audit_observed_bytes"].(float64); !ok || int(observed) != len(oversized) {
		t.Fatalf("request_params._audit_observed_bytes: got %v want %d", rp["_audit_observed_bytes"], len(oversized))
	}
	if invalid, _ := payload["request_params_invalid"].(bool); invalid {
		t.Fatalf("request_params_invalid: got true want false (marker is valid JSON)")
	}
}
