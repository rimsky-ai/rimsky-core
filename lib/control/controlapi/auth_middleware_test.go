// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
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
			ID:          shared.UUID{9, 9, 9},
			Name:        "seed",
			KeyHash:     hash[:],
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   clock.Now(),
		}, tx)
	}); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	return authTestHarness{state: state, tables: d.Tables(), plaintext: plaintext}
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
