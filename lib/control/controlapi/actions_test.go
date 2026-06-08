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
		Routes:   []Route{{"GET", "/things"}},
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
	if err := r.Register(ActionEntry{Action: "a:read", Routes: []Route{{"GET", "/a"}}, MCPTools: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	// Same action twice.
	if err := r.Register(ActionEntry{Action: "a:read", Routes: []Route{{"GET", "/x"}}}); err == nil {
		t.Fatalf("expected duplicate-action error")
	}
	// Same route under a different action.
	if err := r.Register(ActionEntry{Action: "b:read", Routes: []Route{{"GET", "/a"}}}); err == nil {
		t.Fatalf("expected route-collision error")
	}
	// Same MCP tool under a different action.
	if err := r.Register(ActionEntry{Action: "c:read", Routes: []Route{{"GET", "/c"}}, MCPTools: []string{"a"}}); err == nil {
		t.Fatalf("expected tool-collision error")
	}
}

// TestValidateGrantScope_RejectsUnknownDimension proves the mint-time
// scope-dimension validator catches an unknown scope key on an action
// whose ScopeDimensions list is non-empty. Regression target: a typo
// like `{action: "template:register", scope: {"templet_tag": "x"}}`
// (missing 'a' in templet) silently denied every request before this
// validator existed.
func TestValidateGrantScope_RejectsUnknownDimension(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action:          "thing:register",
		Routes:          []Route{{"POST", "/things"}},
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

// TestValidateGrantScope_RejectsScopeOnNonscopeableAction proves the
// validator rejects ANY non-empty scope on an action whose
// ScopeDimensions list is empty (no scopeable dimension exists; the
// grant author is mis-using scope). Example: `{action: "auth:read",
// scope: {"foo": "bar"}}`.
func TestValidateGrantScope_RejectsScopeOnNonscopeableAction(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action: "thing:read",
		Routes: []Route{{"GET", "/things"}},
		// ScopeDimensions left nil — action has no scopeable dimension.
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

// TestValidateGrantScope_AdmitsKnownDimension is the green-path
// sanity check: an entry whose scope key matches the action's
// ScopeDimensions passes validation.
func TestValidateGrantScope_AdmitsKnownDimension(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action:          "thing:register",
		Routes:          []Route{{"POST", "/things"}},
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

// TestValidateGrantScope_AdmitsEmptyScopeOnUnscopeable proves the
// validator allows an unscoped entry against an unscopeable action —
// no scope is the only legal shape there.
func TestValidateGrantScope_AdmitsEmptyScopeOnUnscopeable(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{
		Action: "thing:read",
		Routes: []Route{{"GET", "/things"}},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r.Build()
	if err := r.ValidateGrantScope(auth.Grant{{Action: "thing:read"}}); err != nil {
		t.Fatalf("ValidateGrantScope: empty scope on unscopeable action must pass; got %v", err)
	}
}

// TestValidateGrantScope_SkipsWildcards proves wildcard-action entries
// (`*`, `<noun>:*`, `*:<verb>`) are NOT rejected at mint time even when
// carrying a scope. The wildcard spans many actions with mixed
// ScopeDimensions, so the validator cannot pick one "correct" set; the
// scope is matched at request time against the routed action.
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

// TestV1Registry_TemplateTagDimensionPopulated pins the per-action
// ScopeDimensions list against the auth_request_target.go switch — the
// dimension annotations on the seven scopeable actions must stay in
// lockstep with the resolver. If a future change adds a new
// scopeable dimension to requestTargets, this test reds and forces a
// matching update to ActionEntry.ScopeDimensions.
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
	if err := r.Register(ActionEntry{Action: "x:y", Routes: []Route{{"GET", "/x"}}}); err == nil {
		t.Fatalf("expected post-build register error")
	}
}

func TestActionRegistry_BadActionString(t *testing.T) {
	r := NewActionRegistry()
	if err := r.Register(ActionEntry{Action: "no-colon"}); err == nil {
		t.Fatalf("expected validation error for action without colon")
	}
}

// TestV1Registry verifies BuildV1Registry produces a registry that
// covers every action listed in the spec table. The supplemental
// `observability:read` action is allowed beyond the spec table.
func TestV1Registry(t *testing.T) {
	r := BuildV1Registry()

	// Spec-table actions (from spec section "Action grammar").
	specTableActions := []string{
		"instance:read", "instance:create", "instance:terminate",
		"template:read", "template:register", "template:deploy",
		"template:undeploy", "template:deregister",
		"tag:read", "tag:create", "tag:set", "tag:delete",
		"node:read", "node:invalidate", "node:reset",
		"message:send", "message:read",
		"event:read",
		"lineage:read", "lineage:prune",
		"parked-node:read",
		"waitset:read",
		"claim-holders:read",
		"backfill:create", "backfill:read", "backfill:cancel",
		"asset:read", "asset:materialize", "asset:delete",
		"diagnostics:read",
		"auth:read", "auth:create", "auth:revoke", "auth:rotate",
	}
	if len(specTableActions) != 34 {
		t.Fatalf("specTableActions length drifted from spec (got %d, want 34)", len(specTableActions))
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
	// Surplus actions in registry beyond the spec table.
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
	// Actions sanctioned by specs beyond the original control-plane spec
	// table are listed here so the cross-check stays loud about
	// truly-unsanctioned additions while admitting the instance-debugger
	// surface added by spec
	// .ok-planner/specs/2026-05-24-instance-debugger-design.md.
	allowed := map[string]bool{
		"observability:read": true,
		"mcp:read":           true,
		// Instance-debugger surface (concept:breakpoint).
		"instance:pause":    true,
		"instance:resume":   true,
		"instance:kill":     true,
		"breakpoint:read":   true,
		"breakpoint:create": true,
		"breakpoint:resume": true,
		"breakpoint:delete": true,
		// Template lint surface (spec 2026-05-28-quality-of-life-features).
		"template:validate": true,
		// Audit read surface (spec 2026-05-29-console-upstream-auth-audit-and-fixes):
		// the auth.* slice of the event log, granted separately from event:read.
		"audit:read": true,
		// Compose-origin capability marker: gates the privileged
		// `compose:<project>:<...>` reserved-prefix bypass on tag-create
		// / instance-create. No route maps to this action; CheckGrant
		// consults it server-side when the X-Rimsky-Compose-Origin
		// header is stamped. Only the compose-CLI's privileged key
		// carries it.
		"compose:origin": true,
	}
	for _, a := range surplus {
		if !allowed[a] {
			t.Errorf("V1 registry has surplus action %q not in spec and not in the allowed-supplement list", a)
		}
	}
}

// TestBuiltinSchemasLockstep guards the lockstep the builtinSchemas
// doc-comment promises: every WRITE tool advertised in v1Actions must
// carry an explicit (non-fallback) inputSchema so an MCP client can
// validate args before round-tripping. Read tools may fall back to the
// generic `{"type":"object"}` (argument-free list tools are fine), but a
// write tool with no shape silently degrades to the generic object and
// the client loses argument validation. Without this guard the gap is
// invisible (the catalog defaults a missing entry to the generic shape).
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

// TestRegistryCoversRouter walks every route registered under the
// gated route group and asserts the action registry knows about it.
// The spec made this cross-check load-bearing (plan D7): without it,
// a future route registered without `gate()` would silently bypass
// the audit + auth pipeline and the only signal would be a missing
// event-log row.
//
// Builds a synthetic router by calling each registerXxxRoutes helper
// against a chi.Router seeded with AuthState (so `gate()` returns the
// auth-aware HandlerFunc). The walk only checks route presence; no
// request ever reaches a handler.
//
// Exempts:
//   - GET /health (predates auth; serves load-balancer + k8s probes)
//   - /v1/observability/* (wildcard chi pattern; covered by the
//     `observability:read` umbrella action)
//
// Failure modes the test catches:
//   - A new (METHOD, PATTERN) wired into a register* helper without
//     a matching `Routes:` entry in v1Actions.
//   - A route accidentally mounted in the gated route group without
//     a corresponding action — this would not be gated and would
//     produce no audit row.
func TestRegistryCoversRouter(t *testing.T) {
	state := &AuthState{
		Tables:   nil, // unused during route registration
		Registry: BuildV1Registry(),
		Clock:    shared.SystemClock{},
		Logger:   shared.SilentLogger{},
	}
	deps := AppDeps{
		AuthState: state,
	}
	r := chi.NewRouter()
	registerTemplatesRoutes(r, deps)
	registerTagsRoutes(r, deps)
	registerInstancesRoutes(r, deps)
	registerBreakpointsRoutes(r, deps)
	registerNodesRoutes(r, deps)
	registerEventsRoutes(r, deps)
	registerAuditRoutes(r, deps)
	registerClaimsRoutes(r, deps)
	registerMessagesRoutes(r, deps)
	registerBackfillsRoutes(r, deps)
	registerAssetsRoutes(r, deps)
	registerLineageRoutes(r, deps)
	registerAdminDiagnosticsRoutes(r, deps)
	registerAuthRoutes(r, deps)
	registerMCPRoute(r, deps)

	// Build a quick (METHOD pattern → action) lookup from the
	// registry.
	reg := state.Registry
	byRoutePattern := map[string]string{}
	for _, action := range reg.AllActions() {
		entry, _ := reg.Entry(action)
		for _, rt := range entry.Routes {
			byRoutePattern[rt.Method+" "+rt.Path] = action
		}
	}

	exempt := func(method, pattern string) bool {
		if method == http.MethodGet && pattern == "/health" {
			return true
		}
		if strings.HasPrefix(pattern, "/v1/observability") {
			return true
		}
		return false
	}

	var missing []string
	walkErr := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if exempt(method, route) {
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

// TestRegistryRoutesAreActuallyGated complements TestRegistryCoversRouter:
// for every (method, pattern) the registry knows about, mount a real
// router fronted by a NewApp with a live AuthState, seed a single
// active API key (so the deployment is in authenticated mode rather
// than anonymous-mode fallback), and assert that an unauthenticated
// request returns 401. A route registered without `gate()` would
// match the pattern but return its handler's response (200 / 400 /
// 404), which this test catches.
//
// The test substitutes URL parameters with placeholder UUID / string
// values so chi can route the request — the goal is to verify the
// gate runs BEFORE the handler, not to exercise handler logic. The
// gate runs as part of the IdentityResolver middleware which short-
// circuits with 401 before any URL-param decoding.
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
	// Seed a single active key so IsAnonymousMode returns false and
	// no-Bearer requests get the 401 path rather than the anonymous-
	// mode synthetic identity.
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

	exempt := func(method, pattern string) bool {
		if method == http.MethodGet && pattern == "/health" {
			return true
		}
		if strings.HasPrefix(pattern, "/v1/observability") {
			return true
		}
		return false
	}
	var ungated []string
	walkErr := chi.Walk(app.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if exempt(method, route) {
			return nil
		}
		// Replace chi param placeholders with concrete values.
		url := substituteChiParams(route)
		req, err := http.NewRequest(method, srv.URL+url, strings.NewReader("{}"))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		// Intentionally no Authorization header — we want to verify the
		// gate fires before the handler.
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

// TestV1Registry_ExposesDebuggerTools verifies the 6 debugger-surface
// actions added by spec
// .ok-planner/specs/2026-05-24-instance-debugger-design.md auto-expose
// as MCP tools through the existing action-registry → tools/list
// pipeline. Belt-and-braces with TestV1Registry's surplus check —
// that test asserts the action strings exist; this test asserts they
// flow through to the MCP tool catalog so the polling agent can
// discover them.
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

// substituteChiParams replaces chi-style `{name}` URL parameters in a
// path with safe placeholder values. The substitutions are chosen so
// chi's router accepts them (UUIDs for `{id}`-shaped params, opaque
// strings otherwise); they are NOT expected to resolve to real rows
// because the gate-or-401 check fires before any DB lookup.
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
		// Heuristic: param names containing "id" get UUIDs; everything
		// else gets a safe literal. Wildcard suffixes (e.g. `{*}`) get
		// the empty string.
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

// sha256OfString returns the SHA-256 of s as a 32-byte slice. Kept
// inline to avoid pulling crypto/sha256 into actions.go's import block
// for the production code path.
func sha256OfString(s string) []byte {
	h := auth.Hash(s)
	return h[:]
}
