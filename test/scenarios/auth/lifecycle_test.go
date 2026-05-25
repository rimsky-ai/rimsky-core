// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end auth lifecycle scenarios. Boots a control-api over a
// fresh sqlite-backed persistence handle (lightweight, no Docker) and
// exercises:
//
//   - Bootstrap: anonymous mode → admin key minted → authenticated
//   - Permission grants: *:read read-only key denied on writes
//   - Wildcard semantics: noun-prefix and verb-suffix wildcards
//   - First-match-wins: dry-run override appears before wildcard
//   - Rotation: dual-active during grace; sweep revokes after grace
//   - Revoke-last-key guard: 409 unless ?force_leave_anonymous=true
//   - MCP-as-skin: same identity gate via /mcp tools/call
//
// @concept: api-key
// @concept: permission
// @concept: anonymous-mode

package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/fallguyconsulting/rimsky/control/controlapi"
	"github.com/fallguyconsulting/rimsky/foundation/auth"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	_ "github.com/fallguyconsulting/rimsky/foundation/persistence/sqlite"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/runtime"
)

type authFixture struct {
	srv      *httptest.Server
	db       persistence.Database
	state    *controlapi.AuthState
	clock    *shared.ControllableClock
	teardown func()
}

func newAuthFixture(t *testing.T) *authFixture {
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
	clock := shared.NewControllableClock(time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))
	state := &controlapi.AuthState{
		Tables:   d.Tables(),
		Registry: controlapi.BuildV1Registry(),
		Clock:    clock,
		Logger:   shared.SilentLogger{},
	}
	app := controlapi.NewApp(controlapi.AppDeps{
		Persist:   d.Tables(),
		Queue:     d.Queue(),
		Clock:     clock,
		Logger:    shared.SilentLogger{},
		AuthState: state,
	})
	srv := httptest.NewServer(app)
	return &authFixture{
		srv:   srv,
		db:    d,
		state: state,
		clock: clock,
		teardown: func() {
			srv.Close()
			_ = d.Close()
		},
	}
}

func (f *authFixture) Close() { f.teardown() }

// flushAudit drains any pending audit-row inserts so subsequent
// rimsky_events.List calls in tests observe rows that the
// asynchronous dispatcher hasn't yet committed. Tests that don't
// wire EnsureAuditDispatcher get the synchronous-insert fallback in
// insertEvent and this method is a no-op for them; the helper is
// safe to call unconditionally.
func (f *authFixture) flushAudit() {
	// When the test fixture installs the audit dispatcher, the
	// caller must Stop it to drain. Tests in this package use the
	// synchronous fallback, so this is currently a no-op. Kept as a
	// method so the dry-run / audit assertions read clearly and so
	// future tests that opt into the async dispatcher have an
	// obvious extension point.
}

// post helper.
func (f *authFixture) request(t *testing.T, method, path, key string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func TestBootstrap_AnonymousToAuthenticated(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// /auth/status — anonymous mode.
	code, body := f.request(t, "GET", "/auth/status", "", nil)
	if code != 200 || body["mode"] != "anonymous" {
		t.Fatalf("initial status: %d %+v", code, body)
	}

	// Mint admin without Bearer.
	code, body = f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != 201 {
		t.Fatalf("mint admin: %d %+v", code, body)
	}
	adminKey, _ := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("admin plaintext missing: %+v", body)
	}

	// Now status is authenticated.
	code, body = f.request(t, "GET", "/auth/status", adminKey, nil)
	if code != 200 || body["mode"] != "authenticated" {
		t.Fatalf("post-init status: %d %+v", code, body)
	}

	// Unauth request → 401.
	code, body = f.request(t, "GET", "/auth/keys", "", nil)
	if code != 401 {
		t.Fatalf("expected 401 with no key; got %d %+v", code, body)
	}
}

func TestPermissionGrants_ReadOnlyDenyOnWrite(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Mint admin via anonymous.
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)

	// Mint read-only.
	code, body := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "readonly",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	if code != 201 {
		t.Fatalf("mint readonly: %d %+v", code, body)
	}
	roKey := body["plaintext"].(string)

	// GET /auth/keys works.
	code, _ = f.request(t, "GET", "/auth/keys", roKey, nil)
	if code != 200 {
		t.Fatalf("read-only GET: got %d, want 200", code)
	}
	// POST /auth/keys denied.
	code, body = f.request(t, "POST", "/auth/keys", roKey, map[string]any{
		"name":        "another",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != 403 {
		t.Fatalf("read-only POST: got %d, want 403; body=%+v", code, body)
	}
}

func TestPermissionGrants_FirstMatchWinsDryRun(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)

	// Seed a real deployed template so handleCreateInstance's
	// resolveTagOrHash + LockForUpdate both succeed and the request
	// reaches the in-transaction dry-run gate. Without a real
	// template, resolveTagOrHash returns "" BEFORE the transaction
	// and the handler 404s without ever consulting the request's
	// auth mode — which would make this test unable to distinguish
	// "first-match-wins worked" from "validation failed before mode
	// was even checked."
	tplHash := seedDeployedTemplate(t, f, adminKey, "first-match-wins")

	// Direction 1: specific dry-run BEFORE wildcard. Per spec section
	// "First-match-wins grant evaluation" the specific entry must
	// shadow the wildcard for instance:create — the call must run in
	// dry-run mode and return the synthetic 200 envelope rather than
	// a 201 Created.
	code, body := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name": "specific-first",
		"permissions": []map[string]any{
			{"action": "instance:create", "mode": "dry_run"},
			{"action": "*"},
		},
	})
	if code != 201 {
		t.Fatalf("mint specific-first: %d %+v", code, body)
	}
	specificFirstKey, _ := body["plaintext"].(string)
	if specificFirstKey == "" {
		t.Fatalf("expected plaintext key in mint response: %+v", body)
	}

	code, body = f.request(t, "POST", "/instances", specificFirstKey, map[string]any{
		"template": tplHash,
	})
	if code != http.StatusOK {
		t.Fatalf("specific-first: expected 200 dry-run envelope; got %d %+v", code, body)
	}
	if dryRun, _ := body["dry_run"].(bool); !dryRun {
		t.Fatalf("specific-first: expected dry_run:true; got %+v", body)
	}

	// Direction 2: wildcard BEFORE the dry-run override. The wildcard
	// matches first (mode unset → execute), the later dry-run entry
	// must NOT fire — so the call must be a real 201 Created, proving
	// the engine does not scan further once it has a match.
	code, body = f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name": "wildcard-first",
		"permissions": []map[string]any{
			{"action": "*"},
			{"action": "instance:create", "mode": "dry_run"},
		},
	})
	if code != 201 {
		t.Fatalf("mint wildcard-first: %d %+v", code, body)
	}
	wildcardFirstKey, _ := body["plaintext"].(string)
	if wildcardFirstKey == "" {
		t.Fatalf("expected plaintext key in mint response: %+v", body)
	}

	code, body = f.request(t, "POST", "/instances", wildcardFirstKey, map[string]any{
		"template": tplHash,
	})
	if code != http.StatusCreated {
		t.Fatalf("wildcard-first: expected 201 (wildcard match shadows later dry-run); got %d %+v", code, body)
	}
	if dryRun, _ := body["dry_run"].(bool); dryRun {
		t.Fatalf("wildcard-first: dry_run override fired despite wildcard match earlier in list: %+v", body)
	}
}

// seedDeployedTemplate mirrors seedDryRunNode's template flow but
// stops at the deployed-template step (no instance) and returns the
// template hash so callers can target a real /instances POST.
func seedDeployedTemplate(t *testing.T, f *authFixture, adminKey, name string) string {
	t.Helper()
	tplBody := map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes":                 []map[string]any{{"type": "n1"}},
		},
	}
	code, regResp := f.request(t, "POST", "/templates", adminKey, tplBody)
	if code != 201 && code != 200 {
		t.Fatalf("seedDeployedTemplate register: %d %+v", code, regResp)
	}
	hash, _ := regResp["template_id"].(string)
	if hash == "" {
		t.Fatalf("seedDeployedTemplate register missing template_id: %+v", regResp)
	}
	code, depResp := f.request(t, "POST", "/templates/"+hash+"/deploy", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seedDeployedTemplate deploy: %d %+v", code, depResp)
	}
	return hash
}

func TestRotation_DualActiveAndSweep(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)

	// Mint a second key so revoking admin doesn't trip the last-key guard.
	_, _ = f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "second",
		"permissions": []map[string]any{{"action": "*"}},
	})

	// Rotate admin with 1m grace.
	code, body := f.request(t, "POST", "/auth/keys/admin/rotate", adminKey, map[string]any{
		"grace": "1m",
	})
	if code != 200 {
		t.Fatalf("rotate: %d %+v", code, body)
	}
	newKey := body["plaintext"].(string)
	if newKey == "" || newKey == adminKey {
		t.Fatalf("new key must differ from old: %s vs %s", newKey, adminKey)
	}

	// Both keys still work during grace.
	if code, _ := f.request(t, "GET", "/auth/keys", adminKey, nil); code != 200 {
		t.Fatalf("old key during grace: %d", code)
	}
	if code, _ := f.request(t, "GET", "/auth/keys", newKey, nil); code != 200 {
		t.Fatalf("new key during grace: %d", code)
	}

	// Fast-forward past the grace; sweep revokes old key.
	f.clock.Advance(2 * time.Minute)
	n, err := runtime.SweepRotationGrace(context.Background(), f.db.Tables(), f.clock, shared.SilentLogger{})
	if err != nil || n < 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	// Old key now revoked → 401.
	if code, _ := f.request(t, "GET", "/auth/keys", adminKey, nil); code != 401 {
		t.Fatalf("old key after grace: %d (want 401)", code)
	}
	if code, _ := f.request(t, "GET", "/auth/keys", newKey, nil); code != 200 {
		t.Fatalf("new key after grace: %d (want 200)", code)
	}
}

func TestRevokeGuard_RefuseLastKey(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	// Revoke without force → 409.
	code, body := f.request(t, "DELETE", "/auth/keys/admin", adminKey, nil)
	if code != 409 {
		t.Fatalf("revoke without force: %d %+v", code, body)
	}
	// With force → 200; deployment returns to anonymous.
	code, _ = f.request(t, "DELETE", "/auth/keys/admin?force_leave_anonymous=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("revoke with force: %d", code)
	}
	// Anon mode resumed.
	code, body = f.request(t, "GET", "/auth/status", "", nil)
	if code != 200 || body["mode"] != "anonymous" {
		t.Fatalf("post-force-revoke: %d %+v", code, body)
	}
}

func TestMCPSkin_FiltersByGrant(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)
	_, roBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "ro",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	roKey := roBody["plaintext"].(string)

	// tools/list as admin → contains writes.
	code, listResp := f.request(t, "POST", "/mcp", adminKey, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if code != 200 {
		t.Fatalf("admin tools/list: %d %+v", code, listResp)
	}
	tools := listResp["result"].(map[string]any)["tools"].([]any)
	hasWriter := false
	for _, t := range tools {
		if t.(map[string]any)["name"] == "instance_create" {
			hasWriter = true
		}
	}
	if !hasWriter {
		t.Fatalf("admin tools/list should include instance_create")
	}

	// tools/list as read-only → no writes.
	code, listResp = f.request(t, "POST", "/mcp", roKey, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	if code != 200 {
		t.Fatalf("ro tools/list: %d %+v", code, listResp)
	}
	tools = listResp["result"].(map[string]any)["tools"].([]any)
	for _, tl := range tools {
		name := tl.(map[string]any)["name"].(string)
		if name == "instance_create" {
			t.Errorf("ro tools/list should NOT include instance_create")
		}
	}
}

func TestAuditContent_AccessAttemptedKindEmitted(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	// Make some authenticated request.
	_, _ = f.request(t, "GET", "/auth/keys", adminKey, nil)

	// Inspect rimsky_events for auth.access_attempted rows.
	ctx := context.Background()
	var found int
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessAttempted}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		found = len(rl.Events)
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if found == 0 {
		t.Fatalf("expected at least one auth.access_attempted row; got 0")
	}
}

func TestAuditContent_AccessDeniedRevoked(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, b := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := b["plaintext"].(string)
	// Mint a second key so revoke doesn't trip the last-key guard.
	_, secondBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "second",
		"permissions": []map[string]any{{"action": "*"}},
	})
	secondKey := secondBody["plaintext"].(string)
	// Revoke the second key.
	_, _ = f.request(t, "DELETE", "/auth/keys/second", adminKey, nil)
	// Use the revoked plaintext → 401.
	code, _ := f.request(t, "GET", "/auth/keys", secondKey, nil)
	if code != 401 {
		t.Fatalf("revoked key: %d (want 401)", code)
	}
	// auth.access_denied row should exist with denial_reason: revoked_token.
	ctx := context.Background()
	var foundRevoked bool
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessDenied}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			if reason, _ := e.Payload["denial_reason"].(string); reason == string(auth.DenialRevokedToken) {
				foundRevoked = true
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if !foundRevoked {
		t.Fatalf("expected denial_reason=revoked_token in audit log")
	}
}

// TestAuditContent_AccessDeniedNonBearer covers the audit-row
// classification when a client sends an Authorization header that
// rimsky doesn't speak (e.g. `Basic ...`). Per the auth middleware
// the absence of a header → no_token; the presence of a header with
// a non-Bearer scheme → invalid_token. The two cases are
// operator-meaningful: no_token typically indicates an unconfigured
// client, while invalid_token indicates a client that thinks it IS
// authenticating but is sending the wrong shape.
func TestAuditContent_AccessDeniedNonBearer(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	// Mint admin so deployment is no longer in anonymous mode.
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", body)
	}
	// Send a request with a `Basic` scheme — header present, but not
	// `Bearer`. Use net/http directly because f.request only knows
	// the Bearer shape.
	req, _ := http.NewRequest("GET", f.srv.URL+"/auth/keys", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /auth/keys with Basic scheme: %v", err)
	}
	defer resp.Body.Close()
	respBody := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &respBody)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Basic-scheme request: code=%d body=%+v (want 401)", resp.StatusCode, respBody)
	}
	if reason, _ := respBody["denial_reason"].(string); reason != string(auth.DenialInvalidToken) {
		t.Fatalf("Basic-scheme request: denial_reason=%q (want %q)", reason, auth.DenialInvalidToken)
	}

	// Audit log must contain at least one auth.access_denied row with
	// denial_reason=invalid_token (and NOT no_token for this attempt).
	ctx := context.Background()
	var foundInvalid bool
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessDenied}, persistence.ListPagination{Limit: 100}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			if reason, _ := e.Payload["denial_reason"].(string); reason == string(auth.DenialInvalidToken) {
				foundInvalid = true
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if !foundInvalid {
		t.Fatalf("expected denial_reason=invalid_token in audit log for Basic-scheme request")
	}
}

func TestSweepRotationGrace_InvalidatesAnonCache(t *testing.T) {
	// Per the spec's "Anonymous-mode cache invalidation" section,
	// the rotation-grace sweep MUST drop the per-replica
	// `anonCache` when it runs in-process so the next
	// IsAnonymousMode read reflects the post-sweep key count
	// without waiting on the 1s TTL. This test exercises the
	// in-process bridge (`OnAuthMutation` hook registered via
	// `runtime.RegisterAuthMutationHook`).
	f := newAuthFixture(t)
	defer f.Close()
	t.Cleanup(runtime.RegisterAuthMutationHook(f.state.OnAuthMutation))

	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "soon-to-revoke",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	// Mint a second admin so we can rotate the first one without
	// tripping the last-key guard.
	_, _ = f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "permanent",
		"permissions": []map[string]any{{"action": "*"}},
	})
	// Rotate soon-to-revoke with a 1s grace, then fast-forward
	// past the grace.
	code, rotResp := f.request(t, "POST", "/auth/keys/soon-to-revoke/rotate", adminKey, map[string]any{
		"grace": "1s",
	})
	if code != 200 {
		t.Fatalf("rotate: %d %+v", code, rotResp)
	}
	// Prime the anon cache so we can observe invalidation.
	if anon, err := f.state.IsAnonymousMode(context.Background()); err != nil || anon {
		t.Fatalf("pre-sweep anon: anon=%v err=%v", anon, err)
	}
	// Advance past grace + sweep; the hook must drop the cache so
	// the next IsAnonymousMode call re-queries.
	f.clock.Advance(2 * time.Second)
	if _, err := runtime.SweepRotationGrace(context.Background(), f.db.Tables(), f.clock, shared.SilentLogger{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	// Cache was just dropped by OnAuthMutation; another active key
	// remains so anonymous-mode predicate is still false.
	anon, err := f.state.IsAnonymousMode(context.Background())
	if err != nil {
		t.Fatalf("post-sweep IsAnonymousMode: %v", err)
	}
	if anon {
		t.Errorf("post-sweep anon: got true (cache stale?); want false")
	}
}

// TestAnonymousModePredicateCache_InvalidatesOnMint covers plan L6:
// when handleCreateKey mints a key, it must drop the anon-mode cache so
// the next IsAnonymousMode read reflects the post-mint key count
// without waiting on anonCacheTTL. The test freezes the fixture clock
// so TTL expiry cannot account for the flip — the only way the second
// IsAnonymousMode call returns false is via the explicit
// InvalidateAnonCache call inside handleCreateKey.
func TestAnonymousModePredicateCache_InvalidatesOnMint(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Clock is frozen at the fixture's initial time; we never advance it
	// in this test, so the 1s TTL on anonCacheEntry cannot expire on its
	// own. Any flip from anon=true → anon=false MUST come from
	// InvalidateAnonCache, not from TTL.

	// Prime: deployment is anonymous (no keys); cache populated with
	// isAnon=true.
	ctx := context.Background()
	anon, err := f.state.IsAnonymousMode(ctx)
	if err != nil {
		t.Fatalf("pre-mint IsAnonymousMode: %v", err)
	}
	if !anon {
		t.Fatalf("pre-mint anon: got false; want true (deployment has no keys)")
	}

	// Mint admin via anonymous mode. handleCreateKey calls
	// InvalidateAnonCache after the row is inserted.
	code, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != 201 {
		t.Fatalf("mint admin: %d %+v", code, body)
	}

	// Next IsAnonymousMode read: clock unchanged, so TTL is not the
	// reason for any cache miss. The cached entry was just dropped by
	// handleCreateKey, so the next read re-queries APIKeys.ActiveCount,
	// which now returns 1 → isAnon=false.
	anon, err = f.state.IsAnonymousMode(ctx)
	if err != nil {
		t.Fatalf("post-mint IsAnonymousMode: %v", err)
	}
	if anon {
		t.Fatalf("post-mint anon: got true (cache stale?); want false — the create-key handler must invalidate")
	}
}

// TestAnonymousModePredicateCache_InvalidatesOnRevoke covers the same
// invariant from the other side: revoking the last active key (with
// ?force_leave_anonymous=true) must drop the anon-mode cache so the
// next IsAnonymousMode read reflects the deployment returning to
// anonymous mode without waiting on TTL.
func TestAnonymousModePredicateCache_InvalidatesOnRevoke(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Mint admin via anonymous; deployment is now authenticated.
	_, body := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	// Prime the cache with isAnon=false. The clock is frozen from here
	// on so TTL cannot account for any subsequent flip.
	ctx := context.Background()
	anon, err := f.state.IsAnonymousMode(ctx)
	if err != nil {
		t.Fatalf("pre-revoke IsAnonymousMode: %v", err)
	}
	if anon {
		t.Fatalf("pre-revoke anon: got true; want false (deployment has admin key)")
	}

	// Revoke the last active key with force_leave_anonymous=true.
	// handleRevokeKey calls InvalidateAnonCache after the update.
	code, body := f.request(t, "DELETE", "/auth/keys/admin?force_leave_anonymous=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("revoke admin: %d %+v", code, body)
	}

	// Next IsAnonymousMode read: clock unchanged, so the only way the
	// cached `isAnon=false` flips to `isAnon=true` is via the explicit
	// InvalidateAnonCache call inside handleRevokeKey.
	anon, err = f.state.IsAnonymousMode(ctx)
	if err != nil {
		t.Fatalf("post-revoke IsAnonymousMode: %v", err)
	}
	if !anon {
		t.Fatalf("post-revoke anon: got false (cache stale?); want true — the revoke handler must invalidate")
	}
}

// TestMCPSkin_RequiresMCPReadGate covers the `mcp:read` umbrella action
// added to gate `POST /mcp`. A key with `instance:read` but no
// `mcp:read` (and no wildcard covering it) must receive 403 from the
// JSON-RPC dispatch endpoint, since the umbrella is now load-bearing.
// Without this, an `operator`-shape key (explicit per-noun list, no
// wildcard) would silently lose MCP access.
func TestMCPSkin_RequiresMCPReadGate(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)
	// Mint a key that has SOME permission but does NOT cover
	// `mcp:read`. `instance:read` is concrete enough that no wildcard
	// fan-out matches `mcp:read`.
	_, narrowBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name":        "narrow",
		"permissions": []map[string]any{{"action": "instance:read"}},
	})
	narrowKey := narrowBody["plaintext"].(string)

	// POST /mcp must 403 with this key.
	code, body := f.request(t, "POST", "/mcp", narrowKey, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if code != 403 {
		t.Fatalf("expected 403 on POST /mcp without mcp:read; got %d %+v", code, body)
	}
}

// TestMCPSkin_OperatorRoleKeyWorks covers the regression where an
// `operator`-shape key (explicit per-noun action list, no wildcard)
// receives 403 on POST /mcp because the per-noun list does not cover
// the new `mcp:read` umbrella. The role JSON under `control/cli/roles/`
// must include `mcp:read` so operators using the bundled role retain
// MCP access.
func TestMCPSkin_OperatorRoleKeyWorks(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)
	// Mint a key shaped like the bundled `operator` role: explicit
	// per-noun grants, no `*` wildcard, but including `mcp:read`.
	_, opBody := f.request(t, "POST", "/auth/keys", adminKey, map[string]any{
		"name": "operator-shape",
		"permissions": []map[string]any{
			{"action": "instance:*"},
			{"action": "template:*"},
			{"action": "tag:*"},
			{"action": "auth:read"},
			{"action": "mcp:read"},
		},
	})
	opKey := opBody["plaintext"].(string)

	code, body := f.request(t, "POST", "/mcp", opKey, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if code != 200 {
		t.Fatalf("expected 200 on POST /mcp with operator-shape key; got %d %+v", code, body)
	}
}

func TestAnonymousModeBanner_LogsAndStops(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	// Initial check: anonymous, banner fires.
	cap := shared.NewCapturingLogger()
	f.state.Logger = cap
	if !controlapi.CheckAnonymousBanner(context.Background(), f.state) {
		t.Fatalf("expected banner in anonymous mode")
	}
	// The captured logger must contain a WARN entry with the
	// canonical AnonymousModeBannerMessage; the plan made the const
	// load-bearing so we assert text-level equality here rather
	// than just "a warn was emitted".
	var foundBanner bool
	for _, rec := range cap.Records() {
		if rec.Level != "warn" {
			continue
		}
		if msg, _ := rec.Fields["message"].(string); msg == controlapi.AnonymousModeBannerMessage {
			foundBanner = true
			break
		}
	}
	if !foundBanner {
		t.Fatalf("expected captured logger to contain anonymous-mode banner with text %q; got %d records",
			controlapi.AnonymousModeBannerMessage, len(cap.Records()))
	}
	// Mint a key; banner stops.
	_, _ = f.request(t, "POST", "/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	f.state.InvalidateAnonCache()
	if controlapi.CheckAnonymousBanner(context.Background(), f.state) {
		t.Fatalf("banner should not fire after a key is provisioned")
	}
}
