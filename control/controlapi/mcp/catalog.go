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
	"sync/atomic"

	"github.com/fallguyconsulting/rimsky/foundation/auth"
)

// ctxKeyIdentity matches controlapi.ctxKeyIdentity by value (an empty
// struct of the same definition site). Catalog cannot import
// controlapi (back-cycle); the identity lookup runs by reading the
// context value installed by IdentityResolver. To avoid an unexported
// cross-package key, the mcp package consumes identity via an
// interface contract: the caller passes the request whose context
// already carries the identity, and the catalog reads it through the
// IdentityReader closure.
//
// In V1 we expose the identity via a typed context key that mirrors
// controlapi's. The controlapi package is the only writer; mcp is the
// only consumer. Since both packages live in the same repo and the
// design is one-way (controlapi → mcp), we share a small re-export
// in controlapi/mcp/identity.go.

// identityHookFn is the function signature backing the
// IdentityFromContext indirection.
type identityHookFn func(context.Context) (auth.Identity, bool)

// identityHook holds the active resolver atomically. The variable is
// hot-swapped by controlapi.NewApp at startup and by tests via
// SetIdentityHook (which returns a restore closure so the swap is
// race-free even when tests run with -race).
var identityHook atomic.Value // identityHookFn

func init() {
	identityHook.Store(identityHookFn(func(context.Context) (auth.Identity, bool) {
		return auth.Identity{}, false
	}))
}

// loadIdentityHook returns the current resolver.
func loadIdentityHook() identityHookFn {
	v := identityHook.Load()
	if v == nil {
		return func(context.Context) (auth.Identity, bool) { return auth.Identity{}, false }
	}
	return v.(identityHookFn)
}

// IdentityFromContext is the catalog's read-side entry point. The
// function signature is preserved so call sites read naturally.
//
// Deprecated for write access: assignment-style mutation
// (`mcp.IdentityFromContext = ...`) compiles but is not race-safe.
// Use SetIdentityHook from tests; controlapi.NewApp installs the
// production hook via SetIdentityHook as well.
var IdentityFromContext = func(ctx context.Context) (auth.Identity, bool) {
	return loadIdentityHook()(ctx)
}

// SetIdentityHook installs `fn` as the active identity resolver and
// returns a closure that restores the previous resolver. Tests use
// it via `t.Cleanup(SetIdentityHook(myFn))` for race-safe scoping.
func SetIdentityHook(fn func(context.Context) (auth.Identity, bool)) func() {
	prev := identityHook.Load()
	identityHook.Store(identityHookFn(fn))
	return func() {
		if prev == nil {
			identityHook.Store(identityHookFn(func(context.Context) (auth.Identity, bool) {
				return auth.Identity{}, false
			}))
			return
		}
		identityHook.Store(prev)
	}
}

// Catalog implements ToolCatalog by consulting the action registry +
// per-tool input-schema map + a router-dispatch handler.
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
}

// Filtered renders the catalog filtered by the requesting identity's
// grant. A tool is included if the identity's grant matches the
// tool's action under the wildcard rules.
func (c *Catalog) Filtered(r *http.Request) []Tool {
	ident, _ := IdentityFromContext(r.Context())
	out := []Tool{}
	for _, name := range c.Registry.AllTools() {
		entry, ok := c.Registry.EntryForTool(name)
		if !ok {
			continue
		}
		if !auth.CheckGrant(ident.Permissions, entry.Action).Allowed {
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
	// Prefer the canonical (non-admin) route when an action is
	// mapped to multiple routes. Admin variants typically require
	// additional path parameters (e.g. `node:invalidate` exposes
	// both `/nodes/{id}/invalidate` and
	// `/admin/instances/{instance}/nodes/{node_id}/invalidate`); the
	// MCP-canonical route is the shorter one that takes the same
	// parameters the tool name implies.
	route := pickCanonicalRoute(entry.Routes)

	parsedArgs := map[string]json.RawMessage{}
	if len(args) > 0 {
		// `null`-only args are tolerated as empty.
		if !bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
			if err := json.Unmarshal(args, &parsedArgs); err != nil {
				return nil, &Error{Code: CodeInvalidParams, Message: "args must be a JSON object: " + err.Error()}
			}
		}
	}

	path, remaining, err := substitutePathParams(route.Path, parsedArgs)
	if err != nil {
		return nil, &Error{Code: CodeInvalidParams, Message: err.Error()}
	}

	var body io.Reader
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
		}
	}

	inner, err := http.NewRequestWithContext(r.Context(), route.Method, path, body)
	if err != nil {
		return nil, &Error{Code: CodeInternalError, Message: "build inner request: " + err.Error()}
	}
	// Forward Authorization so the inner auth middleware re-runs.
	if bearer := r.Header.Get("Authorization"); bearer != "" {
		inner.Header.Set("Authorization", bearer)
	}
	if body != nil {
		inner.Header.Set("Content-Type", "application/json")
	}
	// Carry the MCP protocol-skin tag so audit records mark the
	// origin correctly. The setter is exposed as a package-level
	// hook (mirrors controlapi.WithProtocolSkin) to keep this
	// package's imports tight.
	if WithProtocolSkin != nil {
		inner = inner.WithContext(WithProtocolSkin(inner.Context(), "mcp"))
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

// WithProtocolSkin is set at startup by controlapi to bridge the
// context-key without a back-import. Catalog.Invoke calls it on the
// inner request's context before dispatching; if nil, the call is a
// no-op (acceptable for tests that don't care about the skin tag).
var WithProtocolSkin func(ctx context.Context, skin string) context.Context

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

// pickCanonicalRoute selects the MCP-canonical route from the
// registry entry's route list. The heuristic is "shortest path
// without an `/admin/` prefix"; admin variants are operator HTTP-
// only and typically require additional path parameters the MCP
// tool name doesn't imply.
func pickCanonicalRoute(routes []RegistryRoute) RegistryRoute {
	pick := routes[0]
	for _, r := range routes {
		// Skip /admin/ routes when a non-admin alternative exists.
		if strings.HasPrefix(pick.Path, "/admin/") && !strings.HasPrefix(r.Path, "/admin/") {
			pick = r
			continue
		}
		if strings.HasPrefix(r.Path, "/admin/") && !strings.HasPrefix(pick.Path, "/admin/") {
			continue
		}
		// Same admin-ness: prefer the shorter path (fewer placeholders).
		if len(r.Path) < len(pick.Path) {
			pick = r
		}
	}
	return pick
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
