// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestActionRegistry_RegisterAndLookup(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action:   "thing:read",
		Routes:   []Route{{Method: "GET", Path: "/things"}},
		MCPTools: []string{"thing_list"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Build()
	if r.ActionForRoute("GET", "/things") != "thing:read" {
		t.Fatalf("ActionForRoute lookup failed")
	}
	if r.ActionForTool("thing_list") != "thing:read" {
		t.Fatalf("ActionForTool lookup failed")
	}
	if !r.IsKnownAction("thing:read") {
		t.Fatalf("IsKnownAction lookup failed")
	}
	if r.IsKnownAction("nope:read") {
		t.Fatalf("IsKnownAction false-positive")
	}
}

func TestActionRegistry_RejectsDuplicates(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{Action: "a:read", Routes: []Route{{Method: "GET", Path: "/a"}}, MCPTools: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(ActionEntry{Action: "a:read", Routes: []Route{{Method: "GET", Path: "/x"}}}); err == nil {
		t.Fatalf("expected duplicate-action error")
	}
	if err := r.Register(ActionEntry{Action: "b:read", Routes: []Route{{Method: "GET", Path: "/a"}}}); err == nil {
		t.Fatalf("expected route-collision error")
	}
	if err := r.Register(ActionEntry{Action: "c:read", Routes: []Route{{Method: "GET", Path: "/c"}}, MCPTools: []string{"a"}}); err == nil {
		t.Fatalf("expected tool-collision error")
	}
}

func TestValidateGrantScope_RejectsUnknownDimension(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action:          "thing:register",
		Routes:          []Route{{Method: "POST", Path: "/things"}},
		ScopeDimensions: []string{"thing_tag"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Build()
	err := r.ValidateGrantScope(auth.Grant{
		{Action: "thing:register", Scope: map[string]string{"templet_tag": "x"}},
	})
	if err == nil {
		t.Fatalf("ValidateGrantScope: want error for unknown scope dimension; got nil")
	}
	if !strings.Contains(err.Error(), "unknown scope dimension") {
		t.Fatalf("error %q must surface the unknown-dimension diagnostic", err.Error())
	}
}

func TestValidateGrantScope_RejectsScopeOnNonscopeableAction(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action: "thing:read",
		Routes: []Route{{Method: "GET", Path: "/things"}},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Build()
	err := r.ValidateGrantScope(auth.Grant{
		{Action: "thing:read", Scope: map[string]string{"foo": "bar"}},
	})
	if err == nil {
		t.Fatalf("ValidateGrantScope: want error for scope on unscopeable action; got nil")
	}
	if !strings.Contains(err.Error(), "does not support scope") {
		t.Fatalf("error %q must surface the does-not-support-scope diagnostic", err.Error())
	}
}

func TestValidateGrantScope_AdmitsKnownDimension(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action:          "thing:register",
		Routes:          []Route{{Method: "POST", Path: "/things"}},
		ScopeDimensions: []string{"thing_tag"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Build()
	err := r.ValidateGrantScope(auth.Grant{
		{Action: "thing:register", Scope: map[string]string{"thing_tag": "x"}},
	})
	if err != nil {
		t.Fatalf("ValidateGrantScope: want nil for in-dimension scope; got %v", err)
	}
}

func TestValidateGrantScope_AdmitsEmptyScopeOnUnscopeable(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action: "thing:read",
		Routes: []Route{{Method: "GET", Path: "/things"}},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Build()
	if err := r.ValidateGrantScope(auth.Grant{{Action: "thing:read"}}); err != nil {
		t.Fatalf("ValidateGrantScope: empty scope on unscopeable action must pass; got %v", err)
	}
}

func TestValidateGrantScope_SkipsWildcards(t *testing.T) {
	r := NewActionRegistry()
	r.Build()
	for _, action := range []string{"*", "template:*", "*:read"} {
		if err := r.ValidateGrantScope(auth.Grant{
			{Action: action, Scope: map[string]string{"anything": "x"}},
		}); err != nil {
			t.Fatalf("wildcard %q with scope: want nil (skipped); got %v", action, err)
		}
	}
}

func TestV1Registry_TemplateTagDimensionPopulated(t *testing.T) {
	r := BuildV1Registry()
	want := []string{
		"template:register",
		"template:deploy",
		"template:undeploy",
		"template:deregister",
		"tag:set",
		"tag:delete",
		"instance:create",
	}
	for _, action := range want {
		e, ok := r.Entry(action)
		if !ok {
			t.Fatalf("action %q missing from registry", action)
		}
		if len(e.ScopeDimensions) != 1 || e.ScopeDimensions[0] != "template_tag" {
			t.Fatalf("action %q ScopeDimensions = %v; want [template_tag]", action, e.ScopeDimensions)
		}
	}
}

func TestActionRegistry_RejectsAfterBuild(t *testing.T) {
	r := NewActionRegistry()
	r.Build()
	if err := r.Register(ActionEntry{Action: "x:y", Routes: []Route{{Method: "GET", Path: "/x"}}}); err == nil {
		t.Fatalf("expected post-build register error")
	}
}

func TestActionRegistry_BadActionString(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{Action: "no-colon"}); err == nil {
		t.Fatalf("expected validation error for action without colon")
	}
}

func TestV1Registry(t *testing.T) {
	r := BuildV1Registry()

	specTableActions := []string{
		"instance:read", "instance:create", "instance:terminate",
		"template:read", "template:register", "template:deploy",
		"template:undeploy", "template:deregister",
		"tag:read", "tag:create", "tag:set", "tag:delete",
		"node:read", "node:reset",
		"message:send", "message:read",
		"event:read",
		"lineage:read", "lineage:prune",
		"parked-node:read",
		"waitset:read",
		"claim-holders:read",
		"asset:read", "asset:delete",
		"diagnostics:read",
		"auth:read", "auth:create", "auth:revoke", "auth:rotate",
	}
	if len(specTableActions) != 29 {
		t.Fatalf("specTableActions length drifted from spec (got %d, want 29)", len(specTableActions))
	}

	got := r.AllActions()
	gotSet := map[string]bool{}
	for _, a := range got {
		gotSet[a] = true
	}
	for _, a := range specTableActions {
		if !gotSet[a] {
			t.Errorf("V1 registry missing spec-table action %q", a)
		}
	}
	specSet := map[string]bool{}
	for _, a := range specTableActions {
		specSet[a] = true
	}
	var surplus []string
	for _, a := range got {
		if !specSet[a] {
			surplus = append(surplus, a)
		}
	}
	sort.Strings(surplus)
	allowed := map[string]bool{
		"observability:read": true,
		"mcp:read":           true,
		"instance:pause":     true,
		"instance:resume":    true,
		"instance:kill":      true,
		"breakpoint:read":    true,
		"breakpoint:create":  true,
		"breakpoint:resume":  true,
		"breakpoint:delete":  true,
		"template:validate":  true,
		"audit:read":         true,
		"compose:origin":     true,
		// @story: frame-origin-audit
		"instance:list-frames": true,
		"instance:read-frame":  true,
		// @story: debug-channel
		"instance:debug-override": true,
		"service:enroll":          true,
	}
	for _, a := range surplus {
		if !allowed[a] {
			t.Errorf("V1 registry has surplus action %q not in spec and not in the allowed-supplement list", a)
		}
	}
}

func TestActionRoutes_PinnedRouteCounts(t *testing.T) {
	r := BuildV1Registry()
	wantRouteCount := map[string]int{
		"instance:read":           2,
		"instance:create":         1,
		"instance:terminate":      1,
		"instance:pause":          1,
		"instance:resume":         1,
		"instance:kill":           1,
		"instance:debug-override": 1,
		"instance:list-frames":    1,
		"instance:read-frame":     1,
		"breakpoint:read":         2,
		"breakpoint:create":       1,
		"breakpoint:resume":       1,
		"breakpoint:delete":       1,
		"template:read":           2,
		"template:validate":       1,
		"template:register":       1,
		"template:deploy":         1,
		"template:undeploy":       1,
		"template:deregister":     1,
		"tag:read":                1,
		"tag:create":              1,
		"tag:set":                 1,
		"tag:delete":              1,
		"node:read":               2,
		"node:reset":              1,
		"message:send":            1,
		"message:read":            2,
		"event:read":              1,
		"audit:read":              1,
		"lineage:read":            8,
		"lineage:prune":           1,
		"parked-node:read":        1,
		"waitset:read":            1,
		"claim-holders:read":      1,
		"asset:read":              4,
		"asset:delete":            1,
		"diagnostics:read":        1,
		"auth:read":               3,
		"auth:create":             1,
		"auth:revoke":             1,
		"auth:rotate":             1,
		"observability:read":      1,
		composeOriginAction:       0,
		"mcp:read":                2,
		"service:enroll":          1,
	}
	got := r.AllActions()
	if len(got) != len(wantRouteCount) {
		t.Fatalf("registry has %d actions, wantRouteCount table has %d; "+
			"add or remove an entry so a new action or route cannot silently accrete without an explicit pin",
			len(got), len(wantRouteCount))
	}
	for _, action := range got {
		entry, ok := r.Entry(action)
		if !ok {
			t.Fatalf("action %q missing from registry entries", action)
		}
		want, ok := wantRouteCount[action]
		if !ok {
			t.Errorf("action %q missing from wantRouteCount; pin its expected route count", action)
			continue
		}
		if len(entry.Routes) != want {
			t.Errorf("action %q has %d routes, want %d (update wantRouteCount deliberately if this is an intended change, not a silently-added alias)",
				action, len(entry.Routes), want)
		}
	}
}

func TestBuiltinSchemasLockstep(t *testing.T) {
	schemas := builtinSchemas()
	for _, e := range v1Actions {
		if !e.IsWrite {
			continue
		}
		for _, tool := range e.MCPTools {
			if len(schemas[tool]) == 0 {
				t.Errorf("write tool %q (action %q) has no builtinSchemas entry; "+
					"add an explicit inputSchema so MCP clients can validate args "+
					"(keep builtinSchemas in lockstep with v1Actions)", tool, e.Action)
			}
		}
	}
}

func TestRegistryCoversRouter(t *testing.T) {
	state := &AuthState{
		Tables:   nil,
		Registry: BuildV1Registry(),
		Clock:    shared.SystemClock{},
		Logger:   shared.SilentLogger{},
	}
	deps := AppDeps{
		AuthState: state,
	}
	r := chi.NewRouter()
	r.Route("/v1", func(v1 chi.Router) {
		registerTemplatesRoutes(v1, deps)
		registerTagsRoutes(v1, deps)
		registerInstancesRoutes(v1, deps)
		registerBreakpointsRoutes(v1, deps)
		registerDebugOverrideRoutes(v1, deps)
		registerNodesRoutes(v1, deps)
		registerEventsRoutes(v1, deps)
		registerAuditRoutes(v1, deps)
		registerClaimsRoutes(v1, deps)
		registerMessagesRoutes(v1, deps)
		registerFramesRoutes(v1, deps)
		registerAssetsRoutes(v1, deps)
		registerLineageRoutes(v1, deps)
		registerAdminDiagnosticsRoutes(v1, deps)
		registerAuthRoutes(v1, deps)
		registerEnrollRoutes(v1, deps)
		registerMCPRoute(v1, deps)
	})

	reg := state.Registry
	byRoutePattern := map[string]string{}
	for _, action := range reg.AllActions() {
		entry, _ := reg.Entry(action)
		for _, rt := range entry.Routes {
			byRoutePattern[rt.Method+" "+rt.Path] = action
		}
	}

	var missing []string
	walkErr := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if gateExemptRoute(method, route) || identityEchoRoute(method, route) {
			return nil
		}
		if _, ok := byRoutePattern[method+" "+route]; !ok {
			missing = append(missing, method+" "+route)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("chi.Walk: %v", walkErr)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("routes mounted but missing from action registry (would bypass gate / audit):\n  %s\nadd a matching entry to v1Actions in actions.go, or list the route as exempt in this test if the omission is intentional.",
			strings.Join(missing, "\n  "))
	}
}

func TestRegistryRoutesAreActuallyGated(t *testing.T) {
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
	clock := shared.NewControllableClock(time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	state := &AuthState{
		Tables:   d.Tables(),
		Registry: BuildV1Registry(),
		Clock:    clock,
		Logger:   shared.SilentLogger{},
	}
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Tables().APIKeys().Insert(ctx, persistence.APIKey{
			ID:          shared.UUID{1, 2, 3},
			Name:        "seed",
			KeyHash:     sha256OfString("seed-plaintext"),
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   clock.Now(),
		}, tx)
	}); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	app := NewApp(AppDeps{
		Persist:   d.Tables(),
		Queue:     d.Queue(),
		Clock:     clock,
		Logger:    shared.SilentLogger{},
		AuthState: state,
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	var ungated []string
	walkErr := chi.Walk(app.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if gateExemptRoute(method, route) {
			return nil
		}
		url := substituteChiParams(route)
		req, err := http.NewRequest(method, srv.URL+url, strings.NewReader("{}"))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			ungated = append(ungated, method+" "+route+" -> "+http.StatusText(resp.StatusCode))
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("chi.Walk: %v", walkErr)
	}
	if len(ungated) > 0 {
		sort.Strings(ungated)
		t.Fatalf("routes returned non-401 with no Authorization header (would bypass gate / audit):\n  %s\nverify each route's registration wraps the handler in gate(deps, action, ...) rather than passing the bare handler.",
			strings.Join(ungated, "\n  "))
	}
}

func TestV1Registry_ExposesDebuggerTools(t *testing.T) {
	r := BuildV1Registry()
	wantTools := []string{
		"instance_pause",
		"instance_resume",
		"breakpoint_list",
		"breakpoint_create",
		"breakpoint_resume_hit",
		"breakpoint_delete",
	}
	allTools := map[string]bool{}
	for _, name := range r.AllTools() {
		allTools[name] = true
	}
	for _, want := range wantTools {
		if !allTools[want] {
			t.Errorf("tool catalog missing %q (action registry didn't auto-expose the debugger surface)", want)
		}
	}
}

func gateExemptRoute(method, pattern string) bool {
	if method == http.MethodGet && pattern == "/v1/health" {
		return true
	}
	return strings.HasPrefix(pattern, "/v1/observability")
}

func identityEchoRoute(method, pattern string) bool {
	return method == http.MethodGet && pattern == "/v1/auth/whoami"
}

func substituteChiParams(route string) string {
	const placeholderUUID = "00000000-0000-0000-0000-000000000000"
	out := route
	for {
		i := strings.Index(out, "{")
		if i < 0 {
			break
		}
		j := strings.Index(out[i:], "}")
		if j < 0 {
			break
		}
		name := out[i+1 : i+j]
		var v string
		switch {
		case strings.HasPrefix(name, "*"):
			v = ""
		case strings.Contains(name, "id") || name == "instance" || name == "node_id":
			v = placeholderUUID
		default:
			v = "placeholder"
		}
		out = out[:i] + v + out[i+j+1:]
	}
	return out
}

func sha256OfString(s string) []byte {
	h := auth.Hash(s)
	return h[:]
}
