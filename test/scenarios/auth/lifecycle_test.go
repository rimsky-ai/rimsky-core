// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"sync"
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
	srv            *httptest.Server
	db             persistence.Database
	state          *controlapi.AuthState
	clock          *shared.ControllableClock
	claimProducers *locks.Registry
	teardown       func()
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	return newAuthFixtureOpts(t, false)
}

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
	claimProducers := locks.NewRegistry()
	deps := controlapi.AppDeps{
		Persist:        d.Tables(),
		Queue:          d.Queue(),
		Clock:          clock,
		Logger:         shared.SilentLogger{},
		LifecycleSubs:  locks.NewLifecycleRegistry(),
		ClaimProducers: claimProducers,
		AuthState:      state,
	}
	if withObservability {
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
		srv:            srv,
		db:             d,
		state:          state,
		clock:          clock,
		claimProducers: claimProducers,
		teardown: func() {
			srv.Close()
			_ = d.Close()
		},
	}
}

func (f *authFixture) Close() { f.teardown() }

type nopObsProber struct{}

func (*nopObsProber) ProbeExecutor(context.Context, string, string, string) (*observability.ObservabilityCapabilities, error) {
	return nil, errObsProbeUnreachable
}

func (*nopObsProber) ProbeClaimProducer(context.Context, string, string, string) (*observability.ObservabilityCapabilities, error) {
	return nil, errObsProbeUnreachable
}

func (*nopObsProber) ProbeClaimProducerDeclaredErrorClasses(context.Context, string, string, string) ([]string, error) {
	return nil, errObsProbeUnreachable
}

var errObsProbeUnreachable = errors.New("unreachable")

func (f *authFixture) flushAudit() {
}

func (f *authFixture) request(t *testing.T, method, path, key string, body any) (int, map[string]any) {
	t.Helper()
	return f.requestWithHeader(t, method, path, key, body, "", "")
}

func (f *authFixture) requestWithHeader(t *testing.T, method, path, key string, body any, headerKey, headerVal string) (int, map[string]any) {
	t.Helper()
	status, _, out := f.requestFull(t, method, path, key, body, headerKey, headerVal)
	return status, out
}

func (f *authFixture) requestFull(t *testing.T, method, path, key string, body any, headerKey, headerVal string) (int, http.Header, map[string]any) {
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
	return resp.StatusCode, resp.Header, out
}

func (f *authFixture) mcpSession(t *testing.T, key string) string {
	t.Helper()
	status, hdr, out := f.requestFull(t, "POST", "/v1/mcp", key, map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
	}, "", "")
	if status != http.StatusOK {
		t.Fatalf("mcp initialize: got %d, want 200: %+v", status, out)
	}
	sid := hdr.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("mcp initialize did not issue an Mcp-Session-Id header")
	}
	return sid
}

func TestBootstrap_AnonymousToAuthenticated(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	code, body := f.request(t, "GET", "/v1/auth/status", "", nil)
	if code != 200 || body["mode"] != "anonymous" {
		t.Fatalf("initial status: %d %+v", code, body)
	}

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

	code, body = f.request(t, "GET", "/v1/auth/status", adminKey, nil)
	if code != 200 || body["mode"] != "authenticated" {
		t.Fatalf("post-init status: %d %+v", code, body)
	}

	code, body = f.request(t, "GET", "/v1/auth/keys", "", nil)
	if code != 401 {
		t.Fatalf("expected 401 with no key; got %d %+v", code, body)
	}
}

func TestPermissionGrants_ReadOnlyDenyOnWrite(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)

	code, body := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "readonly",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	if code != 201 {
		t.Fatalf("mint readonly: %d %+v", code, body)
	}
	roKey := body["plaintext"].(string)

	code, _ = f.request(t, "GET", "/v1/auth/keys", roKey, nil)
	if code != 200 {
		t.Fatalf("read-only GET: got %d, want 200", code)
	}
	code, body = f.request(t, "POST", "/v1/auth/keys", roKey, map[string]any{
		"name":        "another",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != 403 {
		t.Fatalf("read-only POST: got %d, want 403; body=%+v", code, body)
	}
}

func TestPermissionGrants_DryRunFlagPreviewsWrite(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)

	tplHash := seedDeployedTemplate(t, f, adminKey, "dry-run-flag")

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

	code, body = f.request(t, "POST", "/v1/instances?dry_run=true", creatorKey, map[string]any{
		"template": tplHash,
	})
	if code != http.StatusOK {
		t.Fatalf("dry-run create: expected 200 dry-run envelope; got %d %+v", code, body)
	}
	if dryRun, _ := body["dry_run"].(bool); !dryRun {
		t.Fatalf("dry-run create: expected dry_run:true; got %+v", body)
	}

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

func seedDeployedTemplate(t *testing.T, f *authFixture, adminKey, name string) string {
	t.Helper()
	tplBody := map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "1",
			"messages": []map[string]any{
				{"type": "system/invalidate"},
			},
			"nodes": []map[string]any{{"type": "n1"}},
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

func TestObservabilityDashboard_GatedAndPopulated(t *testing.T) {
	f := newAuthFixtureWithObservability(t)
	defer f.Close()

	const summaryPath = "/v1/observability/system/summary"

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	if code, body := f.request(t, "GET", summaryPath, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("no-bearer summary: got %d %+v; want 401", code, body)
	}

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

	code, summary := f.request(t, "GET", summaryPath, readerKey, nil)
	if code != http.StatusOK {
		t.Fatalf("obs-reader summary: got %d %+v; want 200", code, summary)
	}
	if active, _ := summary["instances_active"].(float64); active < 1 {
		t.Fatalf("instances_active = %v; want >= 1 (seeded one instance): %+v", summary["instances_active"], summary)
	}
	if _, ok := summary["node_runs_by_state"].(map[string]any); !ok {
		t.Fatalf("node_runs_by_state missing or wrong shape: %+v", summary["node_runs_by_state"])
	}
	if nodesTotal, _ := summary["nodes_total"].(float64); nodesTotal < 1 {
		t.Fatalf("nodes_total = %v; want >= 1 node from the seeded instance (nodes_total is the per-node count; node_runs_by_state is a separate per-run-row breakdown and can be legitimately all-zero before any node has run): %+v", summary["nodes_total"], summary)
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

	_, _ = f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "second",
		"permissions": []map[string]any{{"action": "*"}},
	})

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

	if code, _ := f.request(t, "GET", "/v1/auth/keys", adminKey, nil); code != 200 {
		t.Fatalf("old key during grace: %d", code)
	}
	if code, _ := f.request(t, "GET", "/v1/auth/keys", newKey, nil); code != 200 {
		t.Fatalf("new key during grace: %d", code)
	}

	f.clock.Advance(2 * time.Minute)
	n, err := runtime.SweepRotationGrace(context.Background(), f.db.Tables(), f.clock, shared.SilentLogger{})
	if err != nil || n < 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
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
	code, body := f.request(t, "DELETE", "/v1/auth/keys/admin", adminKey, nil)
	if code != 409 {
		t.Fatalf("revoke without force: %d %+v", code, body)
	}
	code, _ = f.request(t, "DELETE", "/v1/auth/keys/admin?force_leave_anonymous=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("revoke with force: %d", code)
	}
	code, body = f.request(t, "GET", "/v1/auth/status", "", nil)
	if code != 200 || body["mode"] != "anonymous" {
		t.Fatalf("post-force-revoke: %d %+v", code, body)
	}
}

func TestRevokeGuard_ConcurrentLastKeyRace(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, aBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin-a",
		"permissions": []map[string]any{{"action": "*"}},
	})
	keyA, _ := aBody["plaintext"].(string)
	if keyA == "" {
		t.Fatalf("mint admin-a: %+v", aBody)
	}
	_, bBody := f.request(t, "POST", "/v1/auth/keys", keyA, map[string]any{
		"name":        "admin-b",
		"permissions": []map[string]any{{"action": "*"}},
	})
	keyB, _ := bBody["plaintext"].(string)
	if keyB == "" {
		t.Fatalf("mint admin-b: %+v", bBody)
	}

	ctx := context.Background()
	now := f.clock.Now()
	if n, err := f.db.Tables().APIKeys().ActiveCount(ctx, now, nil); err != nil || n != 2 {
		t.Fatalf("pre-race active count: n=%d err=%v (want 2)", n, err)
	}

	type revoke struct{ bearer, target string }
	reqs := []revoke{
		{bearer: keyA, target: "admin-b"},
		{bearer: keyB, target: "admin-a"},
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, len(reqs))
	errs := make([]error, len(reqs))
	for i, rq := range reqs {
		wg.Add(1)
		go func(i int, rq revoke) {
			defer wg.Done()
			req, err := http.NewRequest("DELETE", f.srv.URL+"/v1/auth/keys/"+rq.target, nil)
			if err != nil {
				errs[i] = err
				return
			}
			req.Header.Set("Authorization", "Bearer "+rq.bearer)
			<-start
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs[i] = err
				return
			}
			_ = resp.Body.Close()
			codes[i] = resp.StatusCode
		}(i, rq)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("revoke request %d transport error: %v", i, err)
		}
	}

	revoked := 0
	for _, c := range codes {
		if c == http.StatusOK {
			revoked++
		}
	}
	if revoked != 1 {
		t.Fatalf("concurrent revoke of two distinct last active keys: %d requests succeeded (codes=%v); want exactly 1 — the last-active-key guard must let only one through", revoked, codes)
	}

	if n, err := f.db.Tables().APIKeys().ActiveCount(ctx, now, nil); err != nil || n != 1 {
		t.Fatalf("post-race active count: n=%d err=%v (want 1) — n==0 proves the TOCTOU let both revokes commit and dropped the deployment into anonymous mode", n, err)
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

	adminSession := f.mcpSession(t, adminKey)
	code, listResp := f.requestWithHeader(t, "POST", "/v1/mcp", adminKey, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}, "Mcp-Session-Id", adminSession)
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

	roSession := f.mcpSession(t, roKey)
	code, listResp = f.requestWithHeader(t, "POST", "/v1/mcp", roKey, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	}, "Mcp-Session-Id", roSession)
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
	_, _ = f.request(t, "GET", "/v1/auth/keys", adminKey, nil)

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
	_, secondBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "second",
		"permissions": []map[string]any{{"action": "*"}},
	})
	secondKey := secondBody["plaintext"].(string)
	_, _ = f.request(t, "DELETE", "/v1/auth/keys/second", adminKey, nil)
	code, _ := f.request(t, "GET", "/v1/auth/keys", secondKey, nil)
	if code != 401 {
		t.Fatalf("revoked key: %d (want 401)", code)
	}
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

func TestAuditContent_AccessDeniedNonBearer(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", body)
	}
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
	f := newAuthFixture(t)
	defer f.Close()
	t.Cleanup(runtime.RegisterAuthMutationHook(f.state.OnAuthMutation))

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "soon-to-revoke",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)
	_, _ = f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "permanent",
		"permissions": []map[string]any{{"action": "*"}},
	})
	code, rotResp := f.request(t, "POST", "/v1/auth/keys/soon-to-revoke/rotate", adminKey, map[string]any{
		"grace": "1s",
	})
	if code != 200 {
		t.Fatalf("rotate: %d %+v", code, rotResp)
	}
	if anon, err := f.state.IsAnonymousMode(context.Background()); err != nil || anon {
		t.Fatalf("pre-sweep anon: anon=%v err=%v", anon, err)
	}
	f.clock.Advance(2 * time.Second)
	if _, err := runtime.SweepRotationGrace(context.Background(), f.db.Tables(), f.clock, shared.SilentLogger{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	anon, err := f.state.IsAnonymousMode(context.Background())
	if err != nil {
		t.Fatalf("post-sweep IsAnonymousMode: %v", err)
	}
	if anon {
		t.Errorf("post-sweep anon: got true (cache stale?); want false")
	}
}

func TestAnonymousModePredicateCache_InvalidatesOnMint(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	ctx := context.Background()
	anon, err := f.state.IsAnonymousMode(ctx)
	if err != nil {
		t.Fatalf("pre-mint IsAnonymousMode: %v", err)
	}
	if !anon {
		t.Fatalf("pre-mint anon: got false; want true (deployment has no keys)")
	}

	code, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != 201 {
		t.Fatalf("mint admin: %d %+v", code, body)
	}

	anon, err = f.state.IsAnonymousMode(ctx)
	if err != nil {
		t.Fatalf("post-mint IsAnonymousMode: %v", err)
	}
	if anon {
		t.Fatalf("post-mint anon: got true (cache stale?); want false — the create-key handler must invalidate")
	}
}

func TestAnonymousModePredicateCache_InvalidatesOnRevoke(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := body["plaintext"].(string)

	ctx := context.Background()
	anon, err := f.state.IsAnonymousMode(ctx)
	if err != nil {
		t.Fatalf("pre-revoke IsAnonymousMode: %v", err)
	}
	if anon {
		t.Fatalf("pre-revoke anon: got true; want false (deployment has admin key)")
	}

	code, body := f.request(t, "DELETE", "/v1/auth/keys/admin?force_leave_anonymous=true", adminKey, nil)
	if code != 200 {
		t.Fatalf("revoke admin: %d %+v", code, body)
	}

	anon, err = f.state.IsAnonymousMode(ctx)
	if err != nil {
		t.Fatalf("post-revoke IsAnonymousMode: %v", err)
	}
	if !anon {
		t.Fatalf("post-revoke anon: got false (cache stale?); want true — the revoke handler must invalidate")
	}
}

func TestMCPSkin_RequiresMCPReadGate(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)
	_, narrowBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "narrow",
		"permissions": []map[string]any{{"action": "instance:read"}},
	})
	narrowKey := narrowBody["plaintext"].(string)

	code, body := f.request(t, "POST", "/v1/mcp", narrowKey, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if code != 403 {
		t.Fatalf("expected 403 on POST /mcp without mcp:read; got %d %+v", code, body)
	}
}

func TestMCPSkin_OperatorRoleKeyWorks(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey := adminBody["plaintext"].(string)
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

	opSession := f.mcpSession(t, opKey)
	code, body := f.requestWithHeader(t, "POST", "/v1/mcp", opKey, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}, "Mcp-Session-Id", opSession)
	if code != 200 {
		t.Fatalf("expected 200 on POST /mcp with operator-shape key; got %d %+v", code, body)
	}
}

func TestMCPSkin_ToolsCallParityCreatesInstance(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	tplHash := seedDeployedTemplate(t, f, adminKey, "mcp-parity")

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

	mcpKey := "ck-mcp"
	adminSession := f.mcpSession(t, adminKey)
	code, mcpResp := f.requestWithHeader(t, "POST", "/v1/mcp", adminKey, map[string]any{
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
	}, "Mcp-Session-Id", adminSession)
	if code != http.StatusOK {
		t.Fatalf("MCP tools/call: expected 200; got %d %+v", code, mcpResp)
	}

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

	assertInstancePersisted(t, f, adminKey, httpInstanceID, tplHash, httpKey)
	assertInstancePersisted(t, f, adminKey, mcpInstanceID, tplHash, mcpKey)

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

type roleEnforceCase struct {
	action    string
	method    string
	path      string
	body      any
	headerKey string
	headerVal string
}

type roleEnforceSpec struct {
	role    string
	allowed roleEnforceCase
	denied  *roleEnforceCase
}

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

func TestRoleTemplates_MintAndEnforce(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin-bootstrap",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	tplHash := seedDeployedTemplate(t, f, adminKey, "gate6-roles")

	specs := []roleEnforceSpec{
		{
			role: "admin",
			allowed: roleEnforceCase{
				action: "template:register", method: "POST", path: "/v1/templates?dry_run=true",
				body: map[string]any{"spec": map[string]any{
					"name": "gate6-admin-dryrun", "version": "1",
					"nodes": []map[string]any{{"type": "n1"}},
				}},
			},
			denied: nil,
		},
		{
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
			role: "publisher-service",
			allowed: roleEnforceCase{
				action: "message:send", method: "POST",
				path: "/v1/instances/00000000-0000-0000-0000-000000000000/messages",
				body: map[string]any{
					"kind":   "ping",
					"sender": "gate6-test",
				},
				headerKey: "Idempotency-Key", headerVal: "gate6-publisher-send",
			},
			denied: &roleEnforceCase{
				action: "instance:read", method: "GET", path: "/v1/instances",
			},
		},
		{
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

			ac := s.allowed
			code, body := f.requestWithHeader(t, ac.method, ac.path, roleKey, ac.body, ac.headerKey, ac.headerVal)
			if code == http.StatusForbidden {
				t.Fatalf("%s allowed action %q (%s %s): got 403; role grant should cover it. body=%+v",
					s.role, ac.action, ac.method, ac.path, body)
			}

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

// @concept: role-template
func TestRoleTemplates_DebugOperatorGrantsBreakpointVerbsAgentSupervisorDoesNot(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin-bootstrap-debug-boundary",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	tplHash := seedDeployedTemplate(t, f, adminKey, "gate6-debug-boundary")
	code, instBody := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{
		"template":     tplHash,
		"instance_key": "gate6-debug-boundary-ck",
	})
	if code != http.StatusCreated {
		t.Fatalf("create instance: got %d %+v", code, instBody)
	}
	instID, _ := instBody["instance_id"].(string)
	if instID == "" {
		t.Fatalf("create instance: missing instance_id: %+v", instBody)
	}

	debugVerbs := []roleEnforceCase{
		{action: "instance:pause", method: "POST", path: "/v1/instances/" + instID + "/pause"},
		{action: "instance:resume", method: "POST", path: "/v1/instances/" + instID + "/resume"},
		{
			action: "breakpoint:create", method: "POST", path: "/v1/instances/" + instID + "/breakpoints",
			body: map[string]any{"checkpoint": "before_dispatch"},
		},
	}

	mintRoleKey := func(t *testing.T, role string) string {
		t.Helper()
		perms := loadRolePermissions(t, role)
		code, mintResp := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
			"name":        role + "-debug-boundary-key",
			"permissions": perms,
		})
		if code != http.StatusCreated {
			t.Fatalf("mint %s-key: got %d %+v; want 201", role, code, mintResp)
		}
		key, _ := mintResp["plaintext"].(string)
		if key == "" {
			t.Fatalf("mint %s-key: missing plaintext: %+v", role, mintResp)
		}
		return key
	}

	t.Run("debug-operator grant covers pause/resume/breakpoint:create", func(t *testing.T) {
		key := mintRoleKey(t, "debug-operator")
		for _, c := range debugVerbs {
			code, body := f.request(t, c.method, c.path, key, c.body)
			if code == http.StatusForbidden {
				t.Fatalf("debug-operator denied action %q (%s %s): got 403; role grant should cover it. body=%+v",
					c.action, c.method, c.path, body)
			}
		}
	})

	t.Run("agent-supervisor grant excludes pause/resume/breakpoint:create", func(t *testing.T) {
		key := mintRoleKey(t, "agent-supervisor")
		for _, c := range debugVerbs {
			code, body := f.request(t, c.method, c.path, key, c.body)
			if code != http.StatusForbidden {
				t.Fatalf("agent-supervisor allowed action %q (%s %s): got %d; role grant must NOT cover it (want 403). body=%+v",
					c.action, c.method, c.path, code, body)
			}
		}
	})
}

func TestAnonymousModeBanner_LogsAndStops(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()
	cap := shared.NewCapturingLogger()
	f.state.Logger = cap
	if !controlapi.CheckAnonymousBanner(context.Background(), f.state) {
		t.Fatalf("expected banner in anonymous mode")
	}
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
	_, _ = f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	f.state.InvalidateAnonCache()
	if controlapi.CheckAnonymousBanner(context.Background(), f.state) {
		t.Fatalf("banner should not fire after a key is provisioned")
	}
}

func TestLifecycle_APIKeyManagement_AcceptanceWalk(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

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

	code, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "second-bootstrap",
		"permissions": []map[string]any{{"action": "*"}},
	})
	if code != http.StatusUnauthorized {
		t.Fatalf("auth init when keys-table non-empty: got %d %+v; want 401 (falsifier: 'auth-init succeeds when the keys table is non-empty')", code, body)
	}

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
	for _, name := range []string{"admin", "scoped-reader"} {
		code, getResp := f.request(t, "GET", "/v1/auth/keys/"+name, adminPlain, nil)
		if code != http.StatusOK {
			t.Fatalf("get key %q: got %d %+v", name, code, getResp)
		}
		if _, hasPlain := getResp["plaintext"]; hasPlain {
			t.Fatalf("get key %q: response leaked `plaintext`: %+v", name, getResp)
		}
	}

	code, revResp := f.request(t, "DELETE", "/v1/auth/keys/scoped-reader", adminPlain, nil)
	if code != http.StatusOK {
		t.Fatalf("revoke scoped key: got %d %+v", code, revResp)
	}
	code, denyResp := f.request(t, "GET", "/v1/auth/keys", scopedPlain, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("revoked key request: got %d %+v; want 401 (falsifier: 'revoke leaves the old plaintext still accepted')", code, denyResp)
	}
	_, status = f.request(t, "GET", "/v1/auth/status", adminPlain, nil)
	if active, _ := status["active_key_count"].(float64); active != 1 {
		t.Fatalf("auth/status (post-revoke) active_key_count = %v; want 1", status["active_key_count"])
	}

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

	if code, b := f.request(t, "GET", "/v1/auth/keys", adminPlain, nil); code != http.StatusOK {
		t.Fatalf("old key inside grace: got %d %+v; want 200 (falsifier: 'grace window collapses to zero')", code, b)
	}
	if code, b := f.request(t, "GET", "/v1/auth/keys", newPlain, nil); code != http.StatusOK {
		t.Fatalf("new key inside grace: got %d %+v; want 200", code, b)
	}

	_, status = f.request(t, "GET", "/v1/auth/status", newPlain, nil)
	if active, _ := status["active_key_count"].(float64); active != 3 {
		t.Fatalf("auth/status (mid-rotation grace) active_key_count = %v; want 3 (old + new + admin-2)", status["active_key_count"])
	}

	f.clock.Advance(graceDur - 1*time.Second)
	if code, b := f.request(t, "GET", "/v1/auth/keys", adminPlain, nil); code != http.StatusOK {
		t.Fatalf("old key at grace-1s: got %d %+v; want 200", code, b)
	}

	f.clock.Advance(2 * time.Second)
	if n, err := runtime.SweepRotationGrace(context.Background(), f.db.Tables(), f.clock, shared.SilentLogger{}); err != nil || n < 1 {
		t.Fatalf("sweep rotation grace: n=%d err=%v", n, err)
	}
	if code, b := f.request(t, "GET", "/v1/auth/keys", adminPlain, nil); code != http.StatusUnauthorized {
		t.Fatalf("old key past grace + sweep: got %d %+v; want 401 (falsifier: 'rotated key grace window never expires')", code, b)
	}
	if code, b := f.request(t, "GET", "/v1/auth/keys", newPlain, nil); code != http.StatusOK {
		t.Fatalf("new key past grace: got %d %+v; want 200", code, b)
	}
	_, status = f.request(t, "GET", "/v1/auth/status", newPlain, nil)
	if active, _ := status["active_key_count"].(float64); active != 2 {
		t.Fatalf("auth/status (post-sweep) active_key_count = %v; want 2", status["active_key_count"])
	}
	if mode, _ := status["mode"].(string); mode != "authenticated" {
		t.Fatalf("auth/status (post-sweep) mode = %q; want %q", mode, "authenticated")
	}
}
