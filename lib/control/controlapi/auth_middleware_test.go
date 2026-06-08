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

// authTestHarness wires a live AuthState over a sqlite store with one
// seeded `*`-grant key, and a chi router that runs IdentityResolver +
// gateByAction in front of a probe handler. The probe records the
// resolved request Mode so the test can assert the `?dry_run=true` flag
// drives it. Returns the harness pieces and the seeded key plaintext.
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
	// Mint a structurally-valid plaintext: resolveIdentity runs
	// ValidatePlaintext (prefix + base64url shape) before the hash
	// lookup, so an arbitrary string would be rejected as invalid_token.
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

// lastAttemptedRow returns the most-recent auth.access_attempted audit
// row's payload, failing if none is present.
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

// TestGate_DryRunFlagSetsModeAndReadExecutes verifies the two halves of
// the per-request dry-run flag on a READ action:
//   - `?dry_run=true` resolves ModeFromContext to ModeDryRun inside the
//     handler (the flag, not a grant entry, is the only source of mode).
//   - the read genuinely runs, so the audit row records mode=dry_run with
//     executed=true (a read has no mutation to skip).
func TestGate_DryRunFlagSetsModeAndReadExecutes(t *testing.T) {
	h := newAuthTestHarness(t)

	var observedMode auth.Mode
	probe := func(w http.ResponseWriter, r *http.Request) {
		observedMode = ModeFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	// instance:read is a registered read action (IsWrite=false).
	r.Get("/instances", h.state.gateByAction("instance:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/instances?dry_run=true", nil)
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

// TestGate_StreamingHandlerCanFlush is a regression guard for the GET /mcp
// 500 "streaming unsupported" bug: gateByAction wraps the ResponseWriter in
// a *capturingWriter to record the response status for the audit row, but
// that wrapper must still expose http.Flusher so the downstream SSE stream
// handler (lib/control/controlapi/mcp.serveStream) can flush events. Before
// capturingWriter proxied Flush(), the handler's `w.(http.Flusher)`
// assertion failed under the gate and every GET /mcp returned 500. This
// drives the real action GET /mcp gates on (mcp:read) through the gate over
// a live server — the exact path that previously had no coverage.
func TestGate_StreamingHandlerCanFlush(t *testing.T) {
	h := newAuthTestHarness(t)

	// Probe mirrors the MCP SSE stream handler's contract: it requires an
	// http.Flusher, then writes and flushes one SSE event. Under the gate's
	// capturingWriter, the assertion must succeed.
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
	// mcp:read is a registered read action — the umbrella GET /mcp gates on.
	r.Get("/mcp", h.state.gateByAction("mcp:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp", nil)
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

// TestGate_ExecuteBeatsDryRun_MultiEntryGrant verifies the gate's
// matched-entry resolution is order-independent across two grant entries
// on the same action with different modes. A key holding both a dry_run
// entry and an execute entry for `instance:read` must resolve to
// ModeExecute regardless of the order the entries sit in. Before the
// fix, the gate broke on the first allowed entry inside CheckGrant, so
// flipping the entry order silently downgraded a legitimately execute-
// eligible request to dry_run.
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
			// Build a fresh harness per sub-test so audit rows don't bleed.
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
			r.Get("/instances", state.gateByAction("instance:read", probe))

			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/instances", nil)
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

// TestGate_DefaultModeIsExecute verifies the absence of the flag resolves
// to ModeExecute (the default), and a clean read records executed=true.
func TestGate_DefaultModeIsExecute(t *testing.T) {
	h := newAuthTestHarness(t)

	var observedMode auth.Mode
	probe := func(w http.ResponseWriter, r *http.Request) {
		observedMode = ModeFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
	r := chi.NewRouter()
	r.Use(h.state.IdentityResolver())
	r.Get("/instances", h.state.gateByAction("instance:read", probe))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/instances", nil)
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
