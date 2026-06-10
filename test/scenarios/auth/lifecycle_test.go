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
//   - Dry-run flag: ?dry_run=true previews a write; absent → executes
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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/roles"
	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
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
	return newAuthFixtureOpts(t, false)
}

// newAuthFixtureWithObservability builds the same control-api fixture as
// newAuthFixture but wires the real /v1/observability/* mount. Gate 4
// (spec 2026-06-02-acceptance-coverage-recovery) needs the production
// gate block at controlapi.NewApp to actually mount — that block is
// guarded by `deps.Observability != nil`, so without a non-nil
// ObservabilityRouter the subtree is never registered and the routes
// return 404 instead of traversing the real `observability:read` gate.
// We mount the real observability.Routes over the fixture's persistence
// so the dashboard reads project genuine seeded state, not an empty-DB
// shape.
func newAuthFixtureWithObservability(t *testing.T) *authFixture {
	t.Helper()
	return newAuthFixtureOpts(t, true)
}

func newAuthFixtureOpts(t *testing.T, withObservability bool) *authFixture {
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
	deps := controlapi.AppDeps{
		Persist: d.Tables(),
		Queue:   d.Queue(),
		Clock:   clock,
		Logger:  shared.SilentLogger{},
		// An empty (non-nil) lifecycle registry: store-referencing
		// templates (e.g. the wired fan-out node a backfill target
		// requires) register/deploy cleanly — the referenced store is
		// not subscribed, so the lifecycle fan-out skips it silently
		// rather than failing on a nil registry. No store backend runs
		// in this fixture; the engine never dispatches.
		LifecycleSubs: locks.NewLifecycleRegistry(),
		AuthState:     state,
	}
	if withObservability {
		// Wire the real observability router so the production
		// gate block (`obs.Method("GET", "/v1/observability/*",
		// gateByAction("observability:read", ...))`) mounts. The
		// closure receives the chi.Router the wrapper builds under
		// /v1/observability and registers the real read handlers over
		// the fixture's persistence. NewDiscovery with a nop prober is
		// sufficient — the dashboard endpoints Gate 4 asserts
		// (system/summary) read persisted rows, not live peer probes.
		deps.Observability = func(r chi.Router) {
			observability.Routes(r, observability.Deps{
				Tables:    d.Tables(),
				Queue:     d.Queue(),
				Discovery: observability.NewDiscovery(&nopObsProber{}),
			})
		}
	}
	app := controlapi.NewApp(deps)
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

// nopObsProber satisfies observability.Prober for the Gate-4 fixture:
// every probe reports unreachable. The dashboard reads Gate 4 asserts
// project persisted runtime state, so no real peer probe is needed.
type nopObsProber struct{}

func (*nopObsProber) ProbeExecutor(context.Context, string) (*observability.ObservabilityCapabilities, error) {
	return nil, errObsProbeUnreachable
}

func (*nopObsProber) ProbeStore(context.Context, string) (*observability.ObservabilityCapabilities, error) {
	return nil, errObsProbeUnreachable
}

var errObsProbeUnreachable = errors.New("unreachable")

// flushAudit is a no-op kept for call-site readability. Audit rows are
// written synchronously in the request goroutine
// (controlapi.AuthState.insertEvent), so by the time a request returns
// its audit row is already committed and visible to a subsequent
// rimsky_events.List — there is nothing to drain. The method survives
// so the dry-run / audit assertions that call it read clearly.
func (f *authFixture) flushAudit() {
}

// post helper.
func (f *authFixture) request(t *testing.T, method, path, key string, body any) (int, map[string]any) {
	t.Helper()
	return f.requestWithHeader(t, method, path, key, body, "", "")
}

// requestWithHeader is request plus one optional extra header (e.g.
// Idempotency-Key on message:send). headerKey == "" skips the extra
// header.
func (f *authFixture) requestWithHeader(t *testing.T, method, path, key string, body any, headerKey, headerVal string) (int, map[string]any) {
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
	if headerKey != "" {
		req.Header.Set(headerKey, headerVal)
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
	code, body := f.request(t, "GET", "/v1/auth/status", "", nil)
	if code != 200 || body["mode"] != "anonymous" {
		t.Fatalf("initial status: %d %+v", code, body)
	}

	// Mint admin without Bearer.
	code, body = f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
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
	code, body = f.request(t, "GET", "/v1/auth/status", adminKey, nil)
	if code != 200 || body["mode"] != "authenticated" {
		t.Fatalf("post-init status: %d %+v", code, body)
	}

	// Unauth request → 401.
	code, body = f.request(t, "GET", "/v1/auth/keys", "", nil)
	if code != 401 {
		t.Fatalf("expected 401 with no key; got %d %+v", code, body)
	}
}

func TestPermissionGrants_ReadOnlyDenyOnWrite(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Mint admin via anonymous.
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)

	// Mint read-only.
	code, body := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "readonly",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	if code != 201 {
		t.Fatalf("mint readonly: %d %+v", code, body)
	}
	roKey := body["plaintext"].(string)

	// GET /auth/keys works.
	code, _ = f.request(t, "GET", "/v1/auth/keys", roKey, nil)
	if code != 200 {
		t.Fatalf("read-only GET: got %d, want 200", code)
	}
	// POST /auth/keys denied.
	code, body = f.request(t, "POST", "/v1/auth/keys", roKey, map[string]any{
		"name":        "another",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != 403 {
		t.Fatalf("read-only POST: got %d, want 403; body=%+v", code, body)
	}
}

// TestPermissionGrants_DryRunFlagPreviewsWrite covers the per-request
// dry-run flag (the old per-grant `mode: dry_run` modifier and the
// first-match-wins-for-mode evaluator are gone — Pass 1). The SAME key
// (no mode in its grant) previews when `?dry_run=true` is set and
// executes when it is absent: dry-run is sourced solely from the
// request flag, not from the grant.
func TestPermissionGrants_DryRunFlagPreviewsWrite(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)

	// Seed a real deployed template so handleCreateInstance's
	// resolveTagOrHash + LockForUpdate both succeed and the request
	// reaches the in-transaction dry-run gate. Without a real
	// template, resolveTagOrHash returns "" BEFORE the transaction
	// and the handler 404s without ever consulting the request's
	// auth mode.
	tplHash := seedDeployedTemplate(t, f, adminKey, "dry-run-flag")

	// Mint an ordinary execute-capable key (no mode in the grant).
	code, body := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "creator",
		"permissions": []map[string]any{{"action": "instance:create"}, {"action": "instance:read"}},
	})
	if code != 201 {
		t.Fatalf("mint creator: %d %+v", code, body)
	}
	creatorKey, _ := body["plaintext"].(string)
	if creatorKey == "" {
		t.Fatalf("expected plaintext key in mint response: %+v", body)
	}

	// With ?dry_run=true the SAME key previews — 200 dry-run envelope,
	// no instance created.
	code, body = f.request(t, "POST", "/v1/instances?dry_run=true", creatorKey, map[string]any{
		"template": tplHash,
	})
	if code != http.StatusOK {
		t.Fatalf("dry-run create: expected 200 dry-run envelope; got %d %+v", code, body)
	}
	if dryRun, _ := body["dry_run"].(bool); !dryRun {
		t.Fatalf("dry-run create: expected dry_run:true; got %+v", body)
	}

	// Without the flag the SAME key executes — a real 201 Created.
	code, body = f.request(t, "POST", "/v1/instances", creatorKey, map[string]any{
		"template": tplHash,
	})
	if code != http.StatusCreated {
		t.Fatalf("execute create: expected 201; got %d %+v", code, body)
	}
	if dryRun, _ := body["dry_run"].(bool); dryRun {
		t.Fatalf("execute create: dry_run envelope returned without the flag: %+v", body)
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
	code, regResp := f.request(t, "POST", "/v1/templates", adminKey, tplBody)
	if code != 201 && code != 200 {
		t.Fatalf("seedDeployedTemplate register: %d %+v", code, regResp)
	}
	hash, _ := regResp["template_id"].(string)
	if hash == "" {
		t.Fatalf("seedDeployedTemplate register missing template_id: %+v", regResp)
	}
	code, depResp := f.request(t, "POST", "/v1/templates/"+hash+"/deploy", adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seedDeployedTemplate deploy: %d %+v", code, depResp)
	}
	return hash
}

// TestObservabilityDashboard_GatedAndPopulated covers Gate 4 (spec
// 2026-06-02-acceptance-coverage-recovery): the operator dashboard read
// endpoints are gated behind `observability:read` and project real
// counts over seeded runtime state. Prior coverage
// (lib/control/observability/handler_test.go) mounts a bare router with
// NO auth middleware and asserts shape against an empty DB — the
// production gate is never exercised and no populated read is asserted.
//
// This drives the real controlapi.NewApp mount: the gate denies without
// the grant (401 no-bearer, 403 wrong-grant) and, with the grant, the
// summary's instances_active and a node_counts STATE bucket reflect a
// real instance created over POST /instances. node_counts is keyed by
// node STATE (fresh/stale/running/failed), not node type — see
// observability.handleSystemSummary.
func TestObservabilityDashboard_GatedAndPopulated(t *testing.T) {
	f := newAuthFixtureWithObservability(t)
	defer f.Close()

	const summaryPath = "/v1/observability/system/summary"

	// Bootstrap admin so the deployment leaves anonymous mode and the
	// gate has identities to evaluate. (In anonymous mode every request
	// is allowed; we need authenticated mode for the deny assertions to
	// be meaningful.)
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	// Gate (deny) — no bearer → 401 from the auth middleware. This also
	// proves the observability subtree actually mounted: without the
	// non-nil ObservabilityRouter the path would 404, not 401.
	if code, body := f.request(t, "GET", summaryPath, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("no-bearer summary: got %d %+v; want 401", code, body)
	}

	// Gate (deny) — a key with an unrelated grant (no observability:read,
	// no covering wildcard) → 403 from the real observability:read gate.
	_, narrowBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "no-obs",
		"permissions": []map[string]any{{"action": "instance:read"}},
	})
	narrowKey, _ := narrowBody["plaintext"].(string)
	if narrowKey == "" {
		t.Fatalf("mint no-obs: %+v", narrowBody)
	}
	if code, body := f.request(t, "GET", summaryPath, narrowKey, nil); code != http.StatusForbidden {
		t.Fatalf("wrong-grant summary: got %d %+v; want 403", code, body)
	}

	// Seed a real active instance over the real POST /instances surface
	// so the summary has non-empty runtime state to project. Use the
	// admin key (action "*") for the seed; mint a separate reader key
	// that holds ONLY observability:read for the allow assertion, so the
	// 200 is attributable to that grant and not to admin's wildcard.
	tplHash := seedDeployedTemplate(t, f, adminKey, "gate4-summary")
	code, createResp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{
		"template": tplHash,
	})
	if code != http.StatusCreated {
		t.Fatalf("create instance: got %d %+v; want 201", code, createResp)
	}

	_, readerBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "obs-reader",
		"permissions": []map[string]any{{"action": "observability:read"}},
	})
	readerKey, _ := readerBody["plaintext"].(string)
	if readerKey == "" {
		t.Fatalf("mint obs-reader: %+v", readerBody)
	}

	// Gate (allow) + populated — observability:read traverses the gate
	// (200) and the summary reflects the seeded state.
	code, summary := f.request(t, "GET", summaryPath, readerKey, nil)
	if code != http.StatusOK {
		t.Fatalf("obs-reader summary: got %d %+v; want 200", code, summary)
	}
	if active, _ := summary["instances_active"].(float64); active < 1 {
		t.Fatalf("instances_active = %v; want >= 1 (seeded one instance): %+v", summary["instances_active"], summary)
	}
	// node_counts is keyed by node STATE, not type. A freshly created
	// instance's nodes land in fresh/stale; assert at least one state
	// bucket is populated rather than pinning a specific bucket (which
	// depends on the engine's initial-state policy).
	nodeCounts, ok := summary["node_counts"].(map[string]any)
	if !ok {
		t.Fatalf("node_counts missing or wrong shape: %+v", summary["node_counts"])
	}
	var totalNodes float64
	for _, v := range nodeCounts {
		if n, ok := v.(float64); ok {
			totalNodes += n
		}
	}
	if totalNodes < 1 {
		t.Fatalf("node_counts state buckets all zero (%+v); want >= 1 node from the seeded instance", nodeCounts)
	}
}

func TestRotation_DualActiveAndSweep(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)

	// Mint a second key so revoking admin doesn't trip the last-key guard.
	_, _ = f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "second",
		"permissions": []map[string]any{{"action": "*"}},
	})

	// Rotate admin with 1m grace.
	code, body := f.request(t, "POST", "/v1/auth/keys/admin/rotate", adminKey, map[string]any{
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
	if code, _ := f.request(t, "GET", "/v1/auth/keys", adminKey, nil); code != 200 {
		t.Fatalf("old key during grace: %d", code)
	}
	if code, _ := f.request(t, "GET", "/v1/auth/keys", newKey, nil); code != 200 {
		t.Fatalf("new key during grace: %d", code)
	}

	// Fast-forward past the grace; sweep revokes old key.
	f.clock.Advance(2 * time.Minute)
	n, err := runtime.SweepRotationGrace(context.Background(), f.db.Tables(), f.clock, shared.SilentLogger{})
	if err != nil || n < 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	// Old key now revoked → 401.
	if code, _ := f.request(t, "GET", "/v1/auth/keys", adminKey, nil); code != 401 {
		t.Fatalf("old key after grace: %d (want 401)", code)
	}
	if code, _ := f.request(t, "GET", "/v1/auth/keys", newKey, nil); code != 200 {
		t.Fatalf("new key after grace: %d (want 200)", code)
	}
}

func TestRevokeGuard_RefuseLastKey(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	// Revoke without force → 409.
	code, body := f.request(t, "DELETE", "/v1/auth/keys/admin", adminKey, nil)
	if code != 409 {
		t.Fatalf("revoke without force: %d %+v", code, body)
	}
	// With force → 200; deployment returns to anonymous.
	code, _ = f.request(t, "DELETE", "/v1/auth/keys/admin?force_leave_anonymous=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("revoke with force: %d", code)
	}
	// Anon mode resumed.
	code, body = f.request(t, "GET", "/v1/auth/status", "", nil)
	if code != 200 || body["mode"] != "anonymous" {
		t.Fatalf("post-force-revoke: %d %+v", code, body)
	}
}

func TestMCPSkin_FiltersByGrant(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)
	_, roBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "ro",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	roKey := roBody["plaintext"].(string)

	// tools/list as admin → contains writes.
	code, listResp := f.request(t, "POST", "/v1/mcp", adminKey, map[string]any{
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
	code, listResp = f.request(t, "POST", "/v1/mcp", roKey, map[string]any{
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
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	// Make some authenticated request.
	_, _ = f.request(t, "GET", "/v1/auth/keys", adminKey, nil)

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
	_, b := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := b["plaintext"].(string)
	// Mint a second key so revoke doesn't trip the last-key guard.
	_, secondBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "second",
		"permissions": []map[string]any{{"action": "*"}},
	})
	secondKey := secondBody["plaintext"].(string)
	// Revoke the second key.
	_, _ = f.request(t, "DELETE", "/v1/auth/keys/second", adminKey, nil)
	// Use the revoked plaintext → 401.
	code, _ := f.request(t, "GET", "/v1/auth/keys", secondKey, nil)
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
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
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
	req, _ := http.NewRequest("GET", f.srv.URL+"/v1/auth/keys", nil)
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

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "soon-to-revoke",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	// Mint a second admin so we can rotate the first one without
	// tripping the last-key guard.
	_, _ = f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "permanent",
		"permissions": []map[string]any{{"action": "*"}},
	})
	// Rotate soon-to-revoke with a 1s grace, then fast-forward
	// past the grace.
	code, rotResp := f.request(t, "POST", "/v1/auth/keys/soon-to-revoke/rotate", adminKey, map[string]any{
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
	code, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
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
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
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
	code, body := f.request(t, "DELETE", "/v1/auth/keys/admin?force_leave_anonymous=true", adminKey, nil)
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
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)
	// Mint a key that has SOME permission but does NOT cover
	// `mcp:read`. `instance:read` is concrete enough that no wildcard
	// fan-out matches `mcp:read`.
	_, narrowBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "narrow",
		"permissions": []map[string]any{{"action": "instance:read"}},
	})
	narrowKey := narrowBody["plaintext"].(string)

	// POST /mcp must 403 with this key.
	code, body := f.request(t, "POST", "/v1/mcp", narrowKey, map[string]any{
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
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)
	// Mint a key shaped like the bundled `operator` role: explicit
	// per-noun grants, no `*` wildcard, but including `mcp:read`.
	_, opBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
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

	code, body := f.request(t, "POST", "/v1/mcp", opKey, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if code != 200 {
		t.Fatalf("expected 200 on POST /mcp with operator-shape key; got %d %+v", code, body)
	}
}

// TestMCPSkin_ToolsCallParityCreatesInstance proves that
// `tools/call instance_create` over POST /mcp reaches the SAME real
// control-api handler the HTTP verb POST /instances reaches, and
// creates an equivalent instance. Closes the gap where every prior
// tools/call test used a fakeCatalog — here the fixture wires the real
// catalog (NewApp's registerMCPRoute) whose Invoke re-enters the chi
// router, so the MCP path runs handleCreateInstance for real.
//
// The parity bar is threefold: (1) the MCP result carries the
// instance-create response envelope (instance_id), not a placeholder;
// (2) both paths leave a persisted instance readable over GET
// /instances/{id} with the same template hash and distinct
// instance_keys; (3) the MCP-path call wrote an auth.access_attempted
// audit row tagged protocol_skin=mcp for the instance:create action on
// POST /instances — proving the re-entry carried the MCP skin through
// to the canonical forensic record.
func TestMCPSkin_ToolsCallParityCreatesInstance(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Anonymous bootstrap → admin bearer (action "*" covers both the
	// mcp:read umbrella on POST /mcp and the per-tool instance:create
	// gate the re-entry re-runs).
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	// A real deployed template both paths can instantiate.
	tplHash := seedDeployedTemplate(t, f, adminKey, "mcp-parity")

	// HTTP path: POST /instances with a distinct instance_key.
	httpKey := "ck-http"
	code, httpResp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{
		"template":     tplHash,
		"instance_key": httpKey,
	})
	if code != http.StatusCreated {
		t.Fatalf("HTTP create: expected 201; got %d %+v", code, httpResp)
	}
	httpInstanceID, _ := httpResp["instance_id"].(string)
	if httpInstanceID == "" {
		t.Fatalf("HTTP create: missing instance_id: %+v", httpResp)
	}
	if th, _ := httpResp["template_hash"].(string); th != tplHash {
		t.Fatalf("HTTP create: template_hash %q != seeded %q", th, tplHash)
	}

	// MCP path: tools/call instance_create with a DIFFERENT instance_key
	// so the idempotent (template_hash, instance_key) resolution does
	// not collapse the two creates into one row.
	mcpKey := "ck-mcp"
	code, mcpResp := f.request(t, "POST", "/v1/mcp", adminKey, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "instance_create",
			"arguments": map[string]any{
				"template":     tplHash,
				"instance_key": mcpKey,
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("MCP tools/call: expected 200; got %d %+v", code, mcpResp)
	}

	// The JSON-RPC result wraps the tool output in
	// result.content[0].text as a JSON string (MCP convention). Decode
	// it and assert it is the instance-create envelope — an
	// instance_id and the seeded template_hash — NOT a fakeCatalog
	// {"name": ...} placeholder.
	mcpCreate := decodeMCPToolText(t, mcpResp)
	if _, isPlaceholder := mcpCreate["name"]; isPlaceholder {
		if _, hasID := mcpCreate["instance_id"]; !hasID {
			t.Fatalf("MCP result is a {name:...} placeholder, not a real create envelope: %+v", mcpCreate)
		}
	}
	mcpInstanceID, _ := mcpCreate["instance_id"].(string)
	if mcpInstanceID == "" {
		t.Fatalf("MCP create envelope missing instance_id: %+v", mcpCreate)
	}
	if th, _ := mcpCreate["template_hash"].(string); th != tplHash {
		t.Fatalf("MCP create: template_hash %q != seeded %q", th, tplHash)
	}
	if mcpInstanceID == httpInstanceID {
		t.Fatalf("MCP and HTTP creates collapsed to the same instance %q — distinct instance_keys should yield distinct rows", mcpInstanceID)
	}

	// Parity: both produced a persisted instance readable over the real
	// GET /instances/{id} surface, with the same template hash and the
	// distinct instance_keys each path supplied.
	assertInstancePersisted(t, f, adminKey, httpInstanceID, tplHash, httpKey)
	assertInstancePersisted(t, f, adminKey, mcpInstanceID, tplHash, mcpKey)

	// Audit: the MCP-path call must have written an
	// auth.access_attempted row for the instance:create action on POST
	// /instances tagged protocol_skin=mcp. This proves the re-entry
	// carried the MCP skin through to the event log — the WithProtocolSkin
	// limb — and is not merely the HTTP path's row.
	ctx := context.Background()
	var foundMCPCreate bool
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessAttempted}, persistence.ListPagination{Limit: 200}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			skin, _ := e.Payload["protocol_skin"].(string)
			action, _ := e.Payload["action"].(string)
			path, _ := e.Payload["request_path"].(string)
			method, _ := e.Payload["request_method"].(string)
			if skin == "mcp" && action == "instance:create" && path == "/v1/instances" && method == "POST" {
				foundMCPCreate = true
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if !foundMCPCreate {
		t.Fatalf("expected an auth.access_attempted row tagged protocol_skin=mcp for instance:create on POST /instances (the WithProtocolSkin re-entry path)")
	}
}

// decodeMCPToolText unwraps a JSON-RPC tools/call response into the
// tool's actual output object. The MCP envelope nests the tool result
// in result.content[0].text as a JSON-encoded string.
func decodeMCPToolText(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP response has no result object: %+v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("MCP result has no content array: %+v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("MCP content[0] not an object: %+v", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("MCP content[0].text missing: %+v", first)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("MCP content[0].text is not a JSON object (%q): %v", text, err)
	}
	return out
}

// assertInstancePersisted reads GET /instances/{id} over the real read
// surface and asserts the row exists with the expected template hash
// and instance_key.
func assertInstancePersisted(t *testing.T, f *authFixture, key, instanceID, wantTemplateHash, wantInstanceKey string) {
	t.Helper()
	code, body := f.request(t, "GET", "/v1/instances/"+instanceID, key, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /instances/%s: expected 200; got %d %+v", instanceID, code, body)
	}
	if th, _ := body["template_hash"].(string); th != wantTemplateHash {
		t.Fatalf("instance %s: template_hash %q != %q", instanceID, th, wantTemplateHash)
	}
	if ik, _ := body["instance_key"].(string); ik != wantInstanceKey {
		t.Fatalf("instance %s: instance_key %q != %q", instanceID, ik, wantInstanceKey)
	}
}

// roleEnforceCase declares one route to exercise with a role-minted
// key. method/path/body name the request; an optional header (e.g.
// Idempotency-Key on message:send) is set when headerKey != "".
type roleEnforceCase struct {
	action    string // the canonical action the route gates (documentation only)
	method    string
	path      string
	body      any
	headerKey string
	headerVal string
}

// roleEnforceSpec is one bundled-role row in the mint-and-enforce
// table: a representative action the role's grant covers (allowed) and
// — for every role except admin, whose `*` covers everything — a
// representative action the grant does NOT cover (denied).
type roleEnforceSpec struct {
	role    string
	allowed roleEnforceCase
	denied  *roleEnforceCase // nil for admin (no action is outside `*`)
}

// loadRolePermissions reads a bundled role JSON (the SAME embedded
// bytes the CLI expands at `rimsky auth create-key` time) and returns
// its `permissions` array as a slice of grant entries ready to POST as
// the body of /auth/keys. Reading the real bundle — not a hardcoded
// grant — is what couples this test to the role JSONs: corrupting a
// role's JSON must turn the corresponding assertion red (Task 9).
func loadRolePermissions(t *testing.T, role string) []map[string]any {
	t.Helper()
	data, ok := roles.Load(role)
	if !ok {
		t.Fatalf("bundled role %q not found", role)
	}
	var doc struct {
		Permissions []map[string]any `json:"permissions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("role %q: unmarshal permissions: %v", role, err)
	}
	if len(doc.Permissions) == 0 {
		t.Fatalf("role %q: empty permissions array", role)
	}
	return doc.Permissions
}

// TestRoleTemplates_MintAndEnforce covers Gate 6 (spec
// 2026-06-02-acceptance-coverage-recovery): each bundled role JSON
// expands into a permission grant that, when minted as a real key over
// POST /auth/keys, the real auth gate enforces exactly — a
// representative action the role covers traverses the gate (non-403)
// and a representative action the role does NOT cover is denied (403).
//
// Prior coverage was pure-function only
// (cmd/rimsky/cli/roles/audit_read_coverage_test.go calls
// auth.CheckGrant in-process; TestMCPSkin_OperatorRoleKeyWorks exercises
// one operator-shaped grant incidentally). This drives the full path:
// real role JSON → POST /auth/keys mint → real gateByAction enforcement
// on a real route, systematically per bundled role.
//
// The grant is loaded from the SAME go:embed bundle the CLI uses
// (roles.Load), not a hardcoded copy, so corrupting a role's JSON turns
// its assertion red (the coupling-proof in Task 9). For the denied
// action we deliberately pick one the role's grant does NOT cover and
// assert exactly 403 — the gate's deny path; allowed actions assert
// non-403 (the gate passed, regardless of the handler's own status).
func TestRoleTemplates_MintAndEnforce(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// Anonymous bootstrap → admin bearer. Minting a key leaves
	// anonymous mode, so every subsequent request is evaluated against
	// its own key's grant (in anonymous mode the gate allows all and
	// the deny assertions would be meaningless).
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin-bootstrap",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	// A real deployed template so the operator role's representative
	// allowed action (instance:create, exercised as a dry-run create so
	// it mutates nothing) returns a clean 200 dry-run envelope rather
	// than a 404-for-missing-template. Either way it is non-403 (the
	// gate passed), but the clean 200 makes the allowed-path assertion
	// unambiguous.
	tplHash := seedDeployedTemplate(t, f, adminKey, "gate6-roles")

	specs := []roleEnforceSpec{
		{
			// admin → `*`: covers every action; there is no
			// representative denied action, so only the allowed leg runs.
			role: "admin",
			allowed: roleEnforceCase{
				action: "template:register", method: "POST", path: "/v1/templates?dry_run=true",
				body: map[string]any{"spec": map[string]any{
					"name": "gate6-admin-dryrun", "version": "1",
					"frame_resolution_mode": "serial_queue",
					"nodes":                 []map[string]any{{"type": "n1"}},
				}},
			},
			denied: nil,
		},
		{
			// read-only → `*:read`: covers instance:read; denies the
			// instance:create write.
			role: "read-only",
			allowed: roleEnforceCase{
				action: "instance:read", method: "GET", path: "/v1/instances",
			},
			denied: &roleEnforceCase{
				action: "instance:create", method: "POST", path: "/v1/instances",
				body: map[string]any{"template": tplHash},
			},
		},
		{
			// operator → instance:* (covers instance:create) but NOT
			// auth:create (only auth:read). Exercise the allowed create
			// as a dry-run so it mutates nothing.
			role: "operator",
			allowed: roleEnforceCase{
				action: "instance:create", method: "POST", path: "/v1/instances?dry_run=true",
				body: map[string]any{"template": tplHash},
			},
			denied: &roleEnforceCase{
				action: "auth:create", method: "POST", path: "/v1/auth/keys",
				body: map[string]any{
					"name":        "operator-should-not-mint",
					"permissions": []map[string]any{{"action": "instance:read"}},
				},
			},
		},
		{
			// publisher-service → message:send only. Exercise the
			// allowed send against a nonexistent instance: the gate
			// passes (the role covers message:send), the handler then
			// 404/400s — still non-403. instance:read is outside the
			// grant → 403.
			role: "publisher-service",
			allowed: roleEnforceCase{
				action: "message:send", method: "POST",
				path: "/v1/instances/00000000-0000-0000-0000-000000000000/messages",
				body: map[string]any{
					"kind":        "ping",
					"sender_kind": "operator",
					"sender":      "gate6-test",
				},
				headerKey: "Idempotency-Key", headerVal: "gate6-publisher-send",
			},
			denied: &roleEnforceCase{
				action: "instance:read", method: "GET", path: "/v1/instances",
			},
		},
		{
			// debug-operator → `*:read` + breakpoint/instance writes, but
			// NOT instance:create. instance:read covered; instance:create
			// denied.
			role: "debug-operator",
			allowed: roleEnforceCase{
				action: "instance:read", method: "GET", path: "/v1/instances",
			},
			denied: &roleEnforceCase{
				action: "instance:create", method: "POST", path: "/v1/instances",
				body: map[string]any{"template": tplHash},
			},
		},
		{
			// agent-supervisor → `*:read` + node:invalidate/reset +
			// message:send, but NOT instance:create. instance:read
			// covered; instance:create denied.
			role: "agent-supervisor",
			allowed: roleEnforceCase{
				action: "instance:read", method: "GET", path: "/v1/instances",
			},
			denied: &roleEnforceCase{
				action: "instance:create", method: "POST", path: "/v1/instances",
				body: map[string]any{"template": tplHash},
			},
		},
	}

	// Guard against the bundle drifting out from under the table: every
	// bundled role must be covered by a spec above. If a new role JSON
	// is added without a mint-and-enforce row, fail loudly rather than
	// silently leaving it unproven.
	covered := map[string]bool{}
	for _, s := range specs {
		covered[s.role] = true
	}
	for _, name := range roles.AllNames() {
		if !covered[name] {
			t.Fatalf("bundled role %q has no mint-and-enforce row; add one to the table", name)
		}
	}

	for _, s := range specs {
		s := s
		t.Run(s.role, func(t *testing.T) {
			// Mint a key whose permissions ARE the role's expanded grant,
			// loaded from the real embedded bundle.
			perms := loadRolePermissions(t, s.role)
			code, mintResp := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
				"name":        s.role + "-key",
				"permissions": perms,
			})
			if code != http.StatusCreated {
				t.Fatalf("mint %s-key: got %d %+v; want 201", s.role, code, mintResp)
			}
			roleKey, _ := mintResp["plaintext"].(string)
			if roleKey == "" {
				t.Fatalf("mint %s-key: missing plaintext: %+v", s.role, mintResp)
			}

			// Allowed: the role's representative action must traverse the
			// gate — assert non-403. (The handler's own status varies;
			// 200/201/400/404 all prove the gate passed. A 403 here would
			// mean the grant failed to cover the action it should.)
			ac := s.allowed
			code, body := f.requestWithHeader(t, ac.method, ac.path, roleKey, ac.body, ac.headerKey, ac.headerVal)
			if code == http.StatusForbidden {
				t.Fatalf("%s allowed action %q (%s %s): got 403; role grant should cover it. body=%+v",
					s.role, ac.action, ac.method, ac.path, body)
			}

			// Denied: the representative non-role action must hit the
			// gate's deny path — assert exactly 403.
			if s.denied == nil {
				return
			}
			dc := *s.denied
			code, body = f.requestWithHeader(t, dc.method, dc.path, roleKey, dc.body, dc.headerKey, dc.headerVal)
			if code != http.StatusForbidden {
				t.Fatalf("%s denied action %q (%s %s): got %d; role grant must NOT cover it (want 403). body=%+v",
					s.role, dc.action, dc.method, dc.path, code, body)
			}
		})
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
	_, _ = f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	f.state.InvalidateAnonCache()
	if controlapi.CheckAnonymousBanner(context.Background(), f.state) {
		t.Fatalf("banner should not fire after a key is provisioned")
	}
}

// TestLifecycle_APIKeyManagement_AcceptanceWalk is the end-to-end
// acceptance walk for STORY-api-key-management. It exercises EVERY
// Acceptance clause in a single ordered transcript so a single failure
// localizes to the violated clause, and it pins the three Falsifier
// vectors from the spec:
//
//  1. Revoke leaves the old plaintext still accepted.
//  2. Rotate's grace window collapses to zero (old key dies immediately)
//     or never expires.
//  3. auth-init succeeds when the keys table is non-empty.
//
// The test drives the real `/v1/auth/*` surfaces the `rimsky auth init` /
// `auth create-key` / `auth list` / `auth get` / `auth revoke` /
// `auth rotate` / `auth status` CLI verbs all bottom out in, so it is
// the proof artifact for the executable-proof story.
//
// Why a single ordered transcript: STORY-api-key-management is a
// lifecycle story (bootstrap → mint → list/get → revoke → rotate →
// status). Asserting each clause in isolation could let an unrelated
// regression slip through (e.g. a list call buried under the wrong
// active-key-count). Walking the lifecycle once in order makes the
// failure attribution obvious.
//
// Grace-window observability uses the fixture's ControllableClock: we
// configure a 5s grace, hold the clock just before the boundary to
// prove the old key still works, then advance past the boundary and
// run the sweep to prove the old key is then refused. A wall-clock
// sleep would be slower and racy; the clock here is the harness's
// time-fastforward.
func TestLifecycle_APIKeyManagement_AcceptanceWalk(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	// ----- Acceptance clause 1: fresh deployment is in anonymous mode
	// and auth-status reports it accurately.
	code, status := f.request(t, "GET", "/v1/auth/status", "", nil)
	if code != http.StatusOK {
		t.Fatalf("auth/status (fresh): got %d %+v", code, status)
	}
	if mode, _ := status["mode"].(string); mode != "anonymous" {
		t.Fatalf("auth/status (fresh) mode = %q; want %q", mode, "anonymous")
	}
	if active, _ := status["active_key_count"].(float64); active != 0 {
		t.Fatalf("auth/status (fresh) active_key_count = %v; want 0", status["active_key_count"])
	}

	// ----- Acceptance clause 2: bootstrap admin via `auth init` (POST
	// /v1/auth/keys against anonymous mode) returns plaintext exactly
	// once.
	code, mintResp := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != http.StatusCreated {
		t.Fatalf("auth init (bootstrap admin): got %d %+v", code, mintResp)
	}
	adminPlain, _ := mintResp["plaintext"].(string)
	if adminPlain == "" {
		t.Fatalf("auth init: missing plaintext on mint response: %+v", mintResp)
	}

	// ----- Acceptance clause 3: post-bootstrap auth-status reports
	// authenticated mode AND active_key_count = 1 (the new admin key).
	code, status = f.request(t, "GET", "/v1/auth/status", adminPlain, nil)
	if code != http.StatusOK {
		t.Fatalf("auth/status (post-bootstrap): got %d %+v", code, status)
	}
	if mode, _ := status["mode"].(string); mode != "authenticated" {
		t.Fatalf("auth/status (post-bootstrap) mode = %q; want %q", mode, "authenticated")
	}
	if active, _ := status["active_key_count"].(float64); active != 1 {
		t.Fatalf("auth/status (post-bootstrap) active_key_count = %v; want 1", status["active_key_count"])
	}

	// ----- Falsifier vector 3: auth-init succeeds when the keys table is
	// non-empty. POST /v1/auth/keys ANONYMOUSLY (no Bearer) is the
	// `rimsky auth init` surface; once a key exists the server-side gate
	// (the auth middleware on the auth:create action) MUST refuse with
	// 401. If this assertion turns green the falsifier is live.
	code, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "second-bootstrap",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != http.StatusUnauthorized {
		t.Fatalf("auth init when keys-table non-empty: got %d %+v; want 401 (falsifier: 'auth-init succeeds when the keys table is non-empty')", code, body)
	}

	// ----- Acceptance clause 4: with the admin key, the operator mints
	// scoped keys.
	code, scopedResp := f.request(t, "POST", "/v1/auth/keys", adminPlain, map[string]any{
		"name":        "scoped-reader",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	if code != http.StatusCreated {
		t.Fatalf("mint scoped key: got %d %+v", code, scopedResp)
	}
	scopedPlain, _ := scopedResp["plaintext"].(string)
	if scopedPlain == "" {
		t.Fatalf("mint scoped key: missing plaintext: %+v", scopedResp)
	}

	// ----- Acceptance clause 5: metadata reads never expose plaintext.
	// `list` (GET /v1/auth/keys) and `get` (GET /v1/auth/keys/{name})
	// return the keyDTO shape, which has no plaintext field. Assert at
	// the JSON level so a future struct field rename can't silently
	// regress this — the property is "no `plaintext` JSON key anywhere
	// in the listing".
	code, listResp := f.request(t, "GET", "/v1/auth/keys", adminPlain, nil)
	if code != http.StatusOK {
		t.Fatalf("list keys: got %d %+v", code, listResp)
	}
	keys, _ := listResp["keys"].([]any)
	if len(keys) < 2 {
		t.Fatalf("list keys: got %d keys; want >= 2 (admin + scoped-reader)", len(keys))
	}
	for i, raw := range keys {
		entry, _ := raw.(map[string]any)
		if _, hasPlain := entry["plaintext"]; hasPlain {
			t.Fatalf("list keys: entry[%d] leaked `plaintext` field: %+v", i, entry)
		}
	}
	// Get each by name and assert the same property.
	for _, name := range []string{"admin", "scoped-reader"} {
		code, getResp := f.request(t, "GET", "/v1/auth/keys/"+name, adminPlain, nil)
		if code != http.StatusOK {
			t.Fatalf("get key %q: got %d %+v", name, code, getResp)
		}
		if _, hasPlain := getResp["plaintext"]; hasPlain {
			t.Fatalf("get key %q: response leaked `plaintext`: %+v", name, getResp)
		}
	}

	// ----- Falsifier vector 1 + Acceptance clause 6: revoking the
	// scoped key causes subsequent requests bearing that key to be
	// refused. If the revoke is a no-op (the falsifier) the next call
	// would still 200; we require 401.
	code, revResp := f.request(t, "DELETE", "/v1/auth/keys/scoped-reader", adminPlain, nil)
	if code != http.StatusOK {
		t.Fatalf("revoke scoped key: got %d %+v", code, revResp)
	}
	code, denyResp := f.request(t, "GET", "/v1/auth/keys", scopedPlain, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("revoked key request: got %d %+v; want 401 (falsifier: 'revoke leaves the old plaintext still accepted')", code, denyResp)
	}
	// active_key_count reflects the revoke (admin only → 1).
	_, status = f.request(t, "GET", "/v1/auth/status", adminPlain, nil)
	if active, _ := status["active_key_count"].(float64); active != 1 {
		t.Fatalf("auth/status (post-revoke) active_key_count = %v; want 1", status["active_key_count"])
	}

	// ----- Acceptance clauses 7-8 + Falsifier vector 2: rotate produces
	// new plaintext, old key keeps working through the grace window,
	// then stops. Use a configured short grace and observe both sides
	// of the boundary via the ControllableClock — the plan's load-bearing
	// property.
	//
	// Mint a second admin first so rotating the bootstrap admin does
	// not trip the last-key guard.
	_, secondMint := f.request(t, "POST", "/v1/auth/keys", adminPlain, map[string]any{
		"name":        "admin-2",
		"permissions": []map[string]any{{"action": "*"}},
	})
	admin2Plain, _ := secondMint["plaintext"].(string)
	if admin2Plain == "" {
		t.Fatalf("mint admin-2 (rotate-guard): %+v", secondMint)
	}

	const graceDur = 5 * time.Second
	code, rotResp := f.request(t, "POST", "/v1/auth/keys/admin/rotate", adminPlain, map[string]any{
		"grace": graceDur.String(),
	})
	if code != http.StatusOK {
		t.Fatalf("rotate admin: got %d %+v", code, rotResp)
	}
	newPlain, _ := rotResp["plaintext"].(string)
	if newPlain == "" {
		t.Fatalf("rotate admin: missing plaintext on new key: %+v", rotResp)
	}
	if newPlain == adminPlain {
		t.Fatalf("rotate admin: new plaintext must differ from old (got identical strings)")
	}

	// During grace (clock has NOT advanced past the boundary), BOTH
	// keys must work. This pins the second falsifier vector's
	// "collapses to zero" branch: if the old key fails here, the grace
	// window collapsed to zero.
	if code, b := f.request(t, "GET", "/v1/auth/keys", adminPlain, nil); code != http.StatusOK {
		t.Fatalf("old key inside grace: got %d %+v; want 200 (falsifier: 'grace window collapses to zero')", code, b)
	}
	if code, b := f.request(t, "GET", "/v1/auth/keys", newPlain, nil); code != http.StatusOK {
		t.Fatalf("new key inside grace: got %d %+v; want 200", code, b)
	}

	// auth-status reflects the rotation's dual-active window:
	// admin (old; still active because revoke_at is in the future),
	// admin (new; just inserted), admin-2 → 3 active.
	_, status = f.request(t, "GET", "/v1/auth/status", newPlain, nil)
	if active, _ := status["active_key_count"].(float64); active != 3 {
		t.Fatalf("auth/status (mid-rotation grace) active_key_count = %v; want 3 (old + new + admin-2)", status["active_key_count"])
	}

	// Edge probe: advance to just BEFORE the boundary; old key must
	// still work. This pins the "never expires" branch from the
	// other direction — at the boundary's near edge the window is
	// still open.
	f.clock.Advance(graceDur - 1*time.Second)
	if code, b := f.request(t, "GET", "/v1/auth/keys", adminPlain, nil); code != http.StatusOK {
		t.Fatalf("old key at grace-1s: got %d %+v; want 200", code, b)
	}

	// Advance past the boundary and run the sweep. The sweep marks
	// the old key revoked; subsequent requests bearing it MUST 401.
	// If this stayed 200, the second falsifier vector's
	// "never expires" branch is live.
	f.clock.Advance(2 * time.Second)
	if n, err := runtime.SweepRotationGrace(context.Background(), f.db.Tables(), f.clock, shared.SilentLogger{}); err != nil || n < 1 {
		t.Fatalf("sweep rotation grace: n=%d err=%v", n, err)
	}
	if code, b := f.request(t, "GET", "/v1/auth/keys", adminPlain, nil); code != http.StatusUnauthorized {
		t.Fatalf("old key past grace + sweep: got %d %+v; want 401 (falsifier: 'rotated key grace window never expires')", code, b)
	}
	// New key still works.
	if code, b := f.request(t, "GET", "/v1/auth/keys", newPlain, nil); code != http.StatusOK {
		t.Fatalf("new key past grace: got %d %+v; want 200", code, b)
	}
	// active_key_count is now 2 (admin new + admin-2; old admin revoked).
	_, status = f.request(t, "GET", "/v1/auth/status", newPlain, nil)
	if active, _ := status["active_key_count"].(float64); active != 2 {
		t.Fatalf("auth/status (post-sweep) active_key_count = %v; want 2", status["active_key_count"])
	}
	if mode, _ := status["mode"].(string); mode != "authenticated" {
		t.Fatalf("auth/status (post-sweep) mode = %q; want %q", mode, "authenticated")
	}
}
