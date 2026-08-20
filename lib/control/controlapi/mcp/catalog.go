// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

type Catalog struct {
	Registry Registry

	Router http.Handler

	Description func(reg Registry, name string) string

	Schemas map[string][]byte

	ResolveIdentity func(context.Context) (auth.Identity, bool)

	WithProtocolSkin func(ctx context.Context, skin string) context.Context
}

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
		if !entry.ExemptFromPermission && !auth.HasAnyGrant(ident.Permissions, entry.Action) {
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

// @decision: mcp-http-parity
func (c *Catalog) Invoke(r *http.Request, name string, args json.RawMessage) (any, bool, *Error) {
	entry, ok := c.Registry.EntryForTool(name)
	if !ok {
		return nil, false, &Error{Code: CodeMethodNotFound, Message: "unknown tool: " + name}
	}
	if len(entry.Routes) == 0 {
		return nil, false, &Error{Code: CodeInternalError, Message: "tool has no route: " + name}
	}
	parsedArgs := map[string]json.RawMessage{}
	if len(args) > 0 {
		if !bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
			if err := json.Unmarshal(args, &parsedArgs); err != nil {
				return nil, false, &Error{Code: CodeInvalidParams, Message: "args must be a JSON object: " + err.Error()}
			}
		}
	}

	route := pickCanonicalRoute(name, entry.Routes, parsedArgs)

	path, remaining, err := substitutePathParams(route.Path, parsedArgs)
	if err != nil {
		return nil, false, &Error{Code: CodeInvalidParams, Message: err.Error()}
	}

	idempotencyKey := extractIdempotencyKey(remaining)
	dryRunVal, hasDryRun := extractDryRun(remaining)

	var body io.Reader = http.NoBody
	hasBody := false
	switch route.Method {
	case "GET", "DELETE":
		q, dropped := urlQueryFromRemaining(remaining)
		if len(dropped) > 0 {
			return nil, false, &Error{Code: CodeInvalidParams, Message: "cannot encode object/array-valued argument(s) as query params: " + strings.Join(dropped, ", ")}
		}
		path = appendQueryString(path, q)
	default:
		if len(remaining) > 0 {
			bs, err := json.Marshal(remaining)
			if err != nil {
				return nil, false, &Error{Code: CodeInvalidParams, Message: "marshal body: " + err.Error()}
			}
			body = bytes.NewReader(bs)
			hasBody = true
		}
	}
	if hasDryRun {
		path = appendQueryString(path, url.Values{"dry_run": {dryRunVal}}.Encode())
	}

	innerCtx := context.WithValue(r.Context(), chi.RouteCtxKey, (*chi.Context)(nil))
	inner, err := http.NewRequestWithContext(innerCtx, route.Method, path, body)
	if err != nil {
		return nil, false, &Error{Code: CodeInternalError, Message: "build inner request: " + err.Error()}
	}
	if bearer := r.Header.Get("Authorization"); bearer != "" {
		inner.Header.Set("Authorization", bearer)
	}
	if hasBody {
		inner.Header.Set("Content-Type", "application/json")
	}
	if route.Method == http.MethodPost || route.Method == http.MethodPut {
		if idempotencyKey == "" {
			idempotencyKey = "mcp-" + uuid.NewString()
		}
		inner.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if c.WithProtocolSkin != nil {
		inner = inner.WithContext(c.WithProtocolSkin(inner.Context(), "mcp"))
	}

	rec := httptest.NewRecorder()
	c.Router.ServeHTTP(rec, inner)

	resp := rec.Result()
	defer resp.Body.Close()
	bs, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return map[string]any{
			"status":  resp.StatusCode,
			"error":   true,
			"body":    rawOrString(bs),
			"isError": true,
		}, true, nil
	}
	return rawOrString(bs), false, nil
}

const idempotencyKeyArgName = "idempotency_key"

func extractIdempotencyKey(args map[string]json.RawMessage) string {
	raw, ok := args[idempotencyKeyArgName]
	if !ok {
		return ""
	}
	delete(args, idempotencyKeyArgName)
	var key string
	if err := json.Unmarshal(raw, &key); err != nil {
		return ""
	}
	return key
}

const dryRunArgName = "dry_run"

func extractDryRun(args map[string]json.RawMessage) (string, bool) {
	raw, ok := args[dryRunArgName]
	if !ok {
		return "", false
	}
	delete(args, dryRunArgName)
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil {
		if flag {
			return "true", true
		}
		return "false", true
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str, true
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`), true
}

func appendQueryString(path, q string) string {
	if q == "" {
		return path
	}
	if strings.Contains(path, "?") {
		return path + "&" + q
	}
	return path + "?" + q
}

const pathSuffixArgName = "path_suffix"

// @decision: mcp-http-parity
func substitutePathParams(pattern string, args map[string]json.RawMessage) (string, map[string]json.RawMessage, error) {
	remaining := map[string]json.RawMessage{}
	for k, v := range args {
		remaining[k] = v
	}
	out := pattern
	if strings.HasSuffix(out, "/*") {
		raw, ok := args[pathSuffixArgName]
		if !ok {
			return "", nil, fmt.Errorf("missing path param %q", pathSuffixArgName)
		}
		delete(remaining, pathSuffixArgName)
		suffix := stringFromRaw(raw)
		var escaped []string
		for _, seg := range strings.Split(strings.Trim(suffix, "/"), "/") {
			if seg == "" || seg == "." || seg == ".." {
				return "", nil, fmt.Errorf("%s must be a path below the wildcard route, got %q", pathSuffixArgName, suffix)
			}
			escaped = append(escaped, url.PathEscape(seg))
		}
		out = strings.TrimSuffix(out, "*") + strings.Join(escaped, "/")
	}
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
		out = out[:i] + url.PathEscape(stringFromRaw(raw)) + out[i+j+1:]
		delete(remaining, paramName)
	}
	return out, remaining, nil
}

func stringFromRaw(raw json.RawMessage) string {
	var str string
	if err := json.Unmarshal(raw, &str); err != nil {
		return strings.Trim(string(raw), `"`)
	}
	return str
}

func urlQueryFromRemaining(m map[string]json.RawMessage) (string, []string) {
	values := url.Values{}
	var dropped []string
	for k, v := range m {
		trim := strings.TrimSpace(string(v))
		if trim == "" {
			continue
		}
		if trim[0] == '{' || trim[0] == '[' {
			dropped = append(dropped, k)
			continue
		}
		var str string
		if err := json.Unmarshal(v, &str); err != nil {
			str = strings.Trim(trim, `"`)
		}
		values.Add(k, str)
	}
	sort.Strings(dropped)
	return values.Encode(), dropped
}

func pickCanonicalRoute(toolName string, routes []RegistryRoute, args map[string]json.RawMessage) RegistryRoute {
	for _, r := range routes {
		if r.Tool == toolName {
			return r
		}
	}

	hasNonAdmin := false
	for _, r := range routes {
		if !isAdminRoute(r.Path) {
			hasNonAdmin = true
			break
		}
	}
	candidates := make([]RegistryRoute, 0, len(routes))
	for _, r := range routes {
		if hasNonAdmin && isAdminRoute(r.Path) {
			continue
		}
		candidates = append(candidates, r)
	}

	if narrowed := filterRoutes(candidates, func(r RegistryRoute) bool {
		return placeholdersSatisfied(r.Path, args)
	}); len(narrowed) > 0 {
		candidates = narrowed
	}

	wantItem := !strings.HasSuffix(toolName, "_list")
	if narrowed := filterRoutes(candidates, func(r RegistryRoute) bool {
		return isItemRoute(r.Path) == wantItem
	}); len(narrowed) > 0 {
		candidates = narrowed
	}

	pick := candidates[0]
	for _, r := range candidates[1:] {
		if len(r.Path) < len(pick.Path) {
			pick = r
		}
	}
	return pick
}

func isAdminRoute(path string) bool {
	return strings.Contains(path, "/admin/")
}

func filterRoutes(routes []RegistryRoute, keep func(RegistryRoute) bool) []RegistryRoute {
	out := make([]RegistryRoute, 0, len(routes))
	for _, r := range routes {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

func placeholdersSatisfied(path string, args map[string]json.RawMessage) bool {
	for _, name := range pathPlaceholders(path) {
		if _, ok := args[name]; !ok {
			return false
		}
	}
	return true
}

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

func isItemRoute(path string) bool {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	last := segs[len(segs)-1]
	return len(last) >= 2 && last[0] == '{' && last[len(last)-1] == '}'
}

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
