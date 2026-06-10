// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

// Catalog implements ToolCatalog by consulting the action registry +
// per-tool input-schema map + a router-dispatch handler.
//
// Identity and protocol-skin are injected as fields (ResolveIdentity /
// WithProtocolSkin) by the constructing package — the mcp package needs
// neither a back-import of controlapi nor a package-global mutable hook.
// The constructor (controlapi.registerMCPRoute) supplies closures that
// read/tag the request context; tests construct a Catalog with their own.
type Catalog struct {
	// Registry resolves tool names to actions + routes.
	Registry Registry

	// Router is the in-process chi router the catalog forwards
	// tool-call requests through. The MCP route handler runs inside
	// the same chi pipeline; this re-entry causes the auth gate to
	// run again, providing defense-in-depth.
	Router http.Handler

	// Description resolves a tool name to a human description. May
	// be nil; when nil, the action string is used.
	Description func(reg Registry, name string) string

	// Schemas is an optional per-tool inputSchema map. Missing /
	// nil entries default to `{"type":"object"}`.
	Schemas map[string][]byte

	// ResolveIdentity reads the requesting identity off the request
	// context. Injected by the constructor (controlapi passes its
	// context-reader, which knows the unexported identity key). Nil →
	// no identity (the conservative default: an unidentified caller
	// sees no tools).
	ResolveIdentity func(context.Context) (auth.Identity, bool)

	// WithProtocolSkin tags a context with the given protocol skin so
	// the re-entrant tool dispatch records the "mcp" origin in the
	// audit log. Injected by the constructor; nil → no-op (acceptable
	// for tests that don't assert the skin tag).
	WithProtocolSkin func(ctx context.Context, skin string) context.Context
}

// Filtered renders the catalog filtered by the requesting identity's
// grant. A tool is included if the identity's grant matches the
// tool's action under the wildcard rules.
func (c *Catalog) Filtered(r *http.Request) []Tool {
	var ident auth.Identity
	if c.ResolveIdentity != nil {
		ident, _ = c.ResolveIdentity(r.Context())
	}
	out := []Tool{}
	for _, name := range c.Registry.AllTools() {
		entry, ok := c.Registry.EntryForTool(name)
		if !ok {
			continue
		}
		if !auth.CheckGrant(ident.Permissions, entry.Action, nil).Allowed {
			continue
		}
		schema := c.Schemas[name]
		if len(schema) == 0 {
			schema = []byte(`{"type":"object"}`)
		}
		desc := entry.Description
		if c.Description != nil {
			desc = c.Description(c.Registry, name)
		}
		out = append(out, Tool{
			Name:        name,
			Description: desc,
			InputSchema: schema,
		})
	}
	return out
}

// Invoke runs the named tool by dispatching to its first registered
// HTTP route. Path parameters are substituted from `args`; remaining
// args are passed as a JSON body for write methods. The Authorization
// header from the parent request is forwarded so the inner auth
// middleware re-runs.
func (c *Catalog) Invoke(r *http.Request, name string, args json.RawMessage) (any, *Error) {
	entry, ok := c.Registry.EntryForTool(name)
	if !ok {
		return nil, &Error{Code: CodeMethodNotFound, Message: "unknown tool: " + name}
	}
	if len(entry.Routes) == 0 {
		return nil, &Error{Code: CodeInternalError, Message: "tool has no route: " + name}
	}
	parsedArgs := map[string]json.RawMessage{}
	if len(args) > 0 {
		// `null`-only args are tolerated as empty.
		if !bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
			if err := json.Unmarshal(args, &parsedArgs); err != nil {
				return nil, &Error{Code: CodeInvalidParams, Message: "args must be a JSON object: " + err.Error()}
			}
		}
	}

	// Select the canonical route when an action maps to several. The
	// choice is tool-aware (see pickCanonicalRoute): it skips /admin/
	// variants, prefers the route whose path placeholders the args
	// satisfy, and breaks remaining ties by the tool's `_list`/`_get`
	// suffix — so e.g. `node_list` dispatches to `/instances/{idOrKey}/
	// nodes` and `node_get` to `/nodes/{id}` rather than both landing on
	// the shortest path.
	route := pickCanonicalRoute(name, entry.Routes, parsedArgs)

	path, remaining, err := substitutePathParams(route.Path, parsedArgs)
	if err != nil {
		return nil, &Error{Code: CodeInvalidParams, Message: err.Error()}
	}

	// Default to http.NoBody (a non-nil no-op ReadCloser) rather than a
	// nil io.Reader. http.NewRequestWithContext leaves req.Body nil when
	// passed a nil body, and chi's ServeHTTP does not populate it — so any
	// handler that touches req.Body (defer req.Body.Close(), json.Decode,
	// io.ReadAll) nil-dereferences for GET/DELETE tools and body-less POST
	// tools dispatched through this skin. http.NoBody makes those reads and
	// the deferred Close behave like a real empty-body request.
	var body io.Reader = http.NoBody
	hasBody := false
	switch route.Method {
	case "GET", "DELETE":
		// Query params: each remaining arg becomes a query param.
		// V1 only carries simple scalars; complex objects must be
		// in the path or are unsupported (the action registry's
		// V1 routes don't need them).
		q := url4QueryFromRemaining(remaining)
		if q != "" {
			if strings.Contains(path, "?") {
				path += "&" + q
			} else {
				path += "?" + q
			}
		}
	default:
		// POST / PUT: encode remaining as a JSON body.
		if len(remaining) > 0 {
			bs, err := json.Marshal(remaining)
			if err != nil {
				return nil, &Error{Code: CodeInvalidParams, Message: "marshal body: " + err.Error()}
			}
			body = bytes.NewReader(bs)
			hasBody = true
		}
	}

	// Strip the parent chi.RouteContext from the inner request's context.
	// The parent context carries the rctx populated by the OUTER `/v1/mcp`
	// (or `/mcp`) match: rctx.RoutePath, URLParams, and the routing stack
	// all reflect that prior match. chi's Mux.ServeHTTP reuses any rctx it
	// finds on the context instead of fetching a fresh one from its pool —
	// so the re-entry would route against the stale `RoutePath` (e.g.
	// `/mcp`) at the top-level router and 404. Re-issuing through the chi
	// router post-`/v1/` mount makes this leakage visible; pre-`/v1/` the
	// stale `RoutePath` happened to equal `""` and re-routing matched on
	// `r.URL.Path` by accident. Either way, the right semantics is "fresh
	// re-entry": detach the rctx so chi starts routing from scratch.
	innerCtx := context.WithValue(r.Context(), chi.RouteCtxKey, (*chi.Context)(nil))
	inner, err := http.NewRequestWithContext(innerCtx, route.Method, path, body)
	if err != nil {
		return nil, &Error{Code: CodeInternalError, Message: "build inner request: " + err.Error()}
	}
	// Forward Authorization so the inner auth middleware re-runs.
	if bearer := r.Header.Get("Authorization"); bearer != "" {
		inner.Header.Set("Authorization", bearer)
	}
	if hasBody {
		inner.Header.Set("Content-Type", "application/json")
	}
	// Write tools that emit a message (POST /instances/{id}/messages)
	// require a universal Idempotency-Key header. The skin synthesizes a
	// fresh key per tool call — each MCP invocation is a distinct intent,
	// not a retry — so the dispatch isn't rejected by the idempotency gate.
	// Harmless on routes that don't consult it.
	if route.Method == http.MethodPost || route.Method == http.MethodPut {
		inner.Header.Set("Idempotency-Key", "mcp-"+uuid.NewString())
	}
	// Carry the MCP protocol-skin tag so audit records mark the
	// origin correctly. WithProtocolSkin is the injected tagger
	// (mirrors controlapi.WithProtocolSkin) so this package needs no
	// back-import; nil is a no-op.
	if c.WithProtocolSkin != nil {
		inner = inner.WithContext(c.WithProtocolSkin(inner.Context(), "mcp"))
	}

	rec := httptest.NewRecorder()
	c.Router.ServeHTTP(rec, inner)

	resp := rec.Result()
	defer resp.Body.Close()
	bs, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// Surface as a tool-call result envelope rather than a
		// JSON-RPC error so the LLM gets the body content. The
		// `isError` field follows MCP convention.
		return map[string]any{
			"status":  resp.StatusCode,
			"error":   true,
			"body":    rawOrString(bs),
			"isError": true,
		}, nil
	}
	return rawOrString(bs), nil
}

// substitutePathParams replaces `{name}` placeholders in pattern
// with the matching value from args. The substituted args are
// removed from the returned `remaining` map.
func substitutePathParams(pattern string, args map[string]json.RawMessage) (string, map[string]json.RawMessage, error) {
	remaining := map[string]json.RawMessage{}
	for k, v := range args {
		remaining[k] = v
	}
	out := pattern
	for {
		i := strings.Index(out, "{")
		if i < 0 {
			break
		}
		j := strings.Index(out[i:], "}")
		if j < 0 {
			return "", nil, fmt.Errorf("malformed path pattern: %s", pattern)
		}
		paramName := out[i+1 : i+j]
		raw, ok := args[paramName]
		if !ok {
			return "", nil, fmt.Errorf("missing path param %q", paramName)
		}
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			// Allow numeric / boolean / null / other scalars by
			// re-encoding the raw JSON (`42`, `true`, `null`). Strip
			// any surrounding double-quotes for legacy JSON strings.
			str = strings.Trim(string(raw), `"`)
		}
		// URL-path-escape the substituted value so a hostile / careless
		// caller can't smuggle a `/` or reserved character into the
		// chi route. UUIDs and tag names pass through unchanged; the
		// escape is defense-in-depth for the catch-all case.
		out = out[:i] + url.PathEscape(str) + out[i+j+1:]
		delete(remaining, paramName)
	}
	return out, remaining, nil
}

// url4QueryFromRemaining serializes scalar args as &-joined
// key=value query params with proper URL encoding (so a value like
// `"foo bar"` becomes `foo+bar` rather than tripping chi's path
// router). Skips object/array values (not supported in V1).
func url4QueryFromRemaining(m map[string]json.RawMessage) string {
	values := url.Values{}
	for k, v := range m {
		trim := strings.TrimSpace(string(v))
		if trim == "" {
			continue
		}
		if trim[0] == '{' || trim[0] == '[' {
			continue
		}
		var str string
		if err := json.Unmarshal(v, &str); err != nil {
			str = strings.Trim(trim, `"`)
		}
		values.Add(k, str)
	}
	return values.Encode()
}

// pickCanonicalRoute selects the route an MCP tool dispatches to when its
// action maps to several HTTP routes. The selection is tool-aware:
//
//  1. /admin/ routes are skipped when a non-admin alternative exists — admin
//     variants are operator HTTP-only and take extra path params the tool
//     name doesn't imply.
//  2. Among the rest, prefer routes whose every path placeholder is supplied
//     in args. This routes a by-instance list tool (e.g. `node_list`, which
//     supplies `idOrKey`) to `/instances/{idOrKey}/nodes` rather than the
//     by-id `/nodes/{id}`.
//  3. When several routes are still satisfiable — e.g. both `message_list`
//     and `message_get` supply `id`, and both `/instances/{id}/messages` and
//     `/messages/{id}` match — break the tie by tool-name suffix: a `*_list`
//     tool wants the collection route (path ends in a literal segment); every
//     other tool wants the item route (path ends in a `{placeholder}`).
//  4. Shortest path wins among whatever remains.
//
// A plain shortest-path heuristic mis-routes here: it sends both `_list` and
// `_get` (and `instance_get`/`template_get`) to the shortest route, ignoring
// which entity the tool's argument names.
func pickCanonicalRoute(toolName string, routes []RegistryRoute, args map[string]json.RawMessage) RegistryRoute {
	hasNonAdmin := false
	for _, r := range routes {
		if !strings.HasPrefix(r.Path, "/admin/") {
			hasNonAdmin = true
			break
		}
	}
	candidates := make([]RegistryRoute, 0, len(routes))
	for _, r := range routes {
		if hasNonAdmin && strings.HasPrefix(r.Path, "/admin/") {
			continue
		}
		candidates = append(candidates, r)
	}

	// (2) Prefer routes whose placeholders are all supplied in args.
	if narrowed := filterRoutes(candidates, func(r RegistryRoute) bool {
		return placeholdersSatisfied(r.Path, args)
	}); len(narrowed) > 0 {
		candidates = narrowed
	}

	// (3) Break ties by tool-name suffix: `*_list` → collection route,
	// otherwise → item route (trailing `{placeholder}`).
	wantItem := !strings.HasSuffix(toolName, "_list")
	if narrowed := filterRoutes(candidates, func(r RegistryRoute) bool {
		return isItemRoute(r.Path) == wantItem
	}); len(narrowed) > 0 {
		candidates = narrowed
	}

	// (4) Shortest path among the survivors.
	pick := candidates[0]
	for _, r := range candidates[1:] {
		if len(r.Path) < len(pick.Path) {
			pick = r
		}
	}
	return pick
}

// filterRoutes returns the subset of routes satisfying keep.
func filterRoutes(routes []RegistryRoute, keep func(RegistryRoute) bool) []RegistryRoute {
	out := make([]RegistryRoute, 0, len(routes))
	for _, r := range routes {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// placeholdersSatisfied reports whether every `{param}` in path has a
// matching key in args.
func placeholdersSatisfied(path string, args map[string]json.RawMessage) bool {
	for _, name := range pathPlaceholders(path) {
		if _, ok := args[name]; !ok {
			return false
		}
	}
	return true
}

// pathPlaceholders extracts the `{name}` placeholder names from a chi path
// pattern, using the same `{`/`}` scan as substitutePathParams.
func pathPlaceholders(path string) []string {
	var names []string
	rest := path
	for {
		i := strings.Index(rest, "{")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], "}")
		if j < 0 {
			break
		}
		names = append(names, rest[i+1:i+j])
		rest = rest[i+j+1:]
	}
	return names
}

// isItemRoute reports whether the path's final segment is a placeholder
// (e.g. `/nodes/{id}` — an item route) rather than a literal collection
// segment (e.g. `/instances/{idOrKey}/nodes`).
func isItemRoute(path string) bool {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	last := segs[len(segs)-1]
	return len(last) >= 2 && last[0] == '{' && last[len(last)-1] == '}'
}

// rawOrString decodes the response body as JSON if possible; falls
// back to a string copy otherwise.
func rawOrString(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err == nil {
		return v
	}
	return string(b)
}
