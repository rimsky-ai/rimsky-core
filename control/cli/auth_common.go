// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Shared helpers used by every `rimsky auth ...` subcommand: endpoint
// resolution (flag + env), bundled-role loading + patch application,
// and the Client factory the subcommand handlers use to issue
// /auth/keys requests via Client.RawCall.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/fallguyconsulting/rimsky/control/cli/roles"
	"github.com/fallguyconsulting/rimsky/foundation/auth"
)

// resolveAuthEndpointAndKey returns the control-api endpoint URL and
// the API key (Bearer token) from CLI flag, env var, or rimsky config.
// In anonymous mode (the bootstrap path), key may be empty; the
// server accepts unauthenticated requests until the first key is
// minted.
func resolveAuthEndpointAndKey(flagEndpoint, flagKey string) (string, string, error) {
	cfgPath, _ := DefaultConfigPath()
	endpoint, err := ResolveEndpoint(flagEndpoint, os.Getenv("RIMSKY_CONTROL_API"), cfgPath, "")
	if err != nil {
		return "", "", err
	}
	key := flagKey
	if key == "" {
		key = os.Getenv("RIMSKY_API_KEY")
	}
	return endpoint, key, nil
}

// newAuthClient builds a Client targeting endpoint with the optional
// Bearer key installed. Empty key leaves the Authorization header
// unset (anonymous-mode requests).
func newAuthClient(endpoint, key string) *Client {
	c := NewClient(endpoint)
	if key != "" {
		c.SetAPIKey(key)
	}
	return c
}

// formatAuthAPIError renders a Client.RawCall error in the shape the
// auth subcommands surfaced before the consolidation: `<METHOD> <path>:
// <status> <body-json>`. Used so callers' stderr matches the spec's
// expected human form and the smoke tests pattern-match unchanged.
func formatAuthAPIError(method, path string, err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		body, _ := json.Marshal(apiErr.Body)
		return fmt.Errorf("%s %s: %d %s", method, path, apiErr.Status, strings.TrimSpace(string(body)))
	}
	return err
}

// roleSpec is the deserialized shape of a bundled role JSON.
type roleSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Permissions auth.Grant `json:"permissions"`
}

// loadRole loads a bundled role by name, or a custom role from --role-file.
// At least one of `name` or `path` must be non-empty; if both are set, the
// path wins (operators replacing a bundled role with a local override).
func loadRole(name, path string) (roleSpec, error) {
	var raw []byte
	if path != "" {
		bs, err := os.ReadFile(path)
		if err != nil {
			return roleSpec{}, fmt.Errorf("read --role-file %q: %w", path, err)
		}
		raw = bs
	} else if name != "" {
		bs, ok := roles.Load(name)
		if !ok {
			return roleSpec{}, fmt.Errorf("unknown bundled role %q (available: %s)", name, strings.Join(roles.AllNames(), ", "))
		}
		raw = bs
	} else {
		return roleSpec{}, fmt.Errorf("--role or --role-file is required")
	}
	var r roleSpec
	if err := json.Unmarshal(raw, &r); err != nil {
		return roleSpec{}, fmt.Errorf("parse role JSON: %w", err)
	}
	if r.Name == "" || len(r.Permissions) == 0 {
		return roleSpec{}, fmt.Errorf("role JSON: name and permissions are required")
	}
	return r, nil
}

// applyGrantPatches mutates a grant by applying --add / --remove /
// --dry-run patches. Returns the patched grant.
//
// Validation per spec:
//   - `--dry-run=<action>` rejects read actions (`*:read` ending) and
//     auth-mutation actions (auth:create / revoke / rotate); CLI-side
//     nicety, not a server enforcement.
func applyGrantPatches(grant auth.Grant, add, remove, dryRun []string) (auth.Grant, error) {
	out := append(auth.Grant{}, grant...)

	for _, a := range add {
		if err := auth.ValidateActionString(a); err != nil {
			return nil, fmt.Errorf("--add %q: %w", a, err)
		}
		out = append(out, auth.GrantEntry{Action: a, Mode: auth.ModeExecute})
	}

	for _, a := range remove {
		filtered := out[:0]
		for _, e := range out {
			if e.Action != a {
				filtered = append(filtered, e)
			}
		}
		out = filtered
	}

	for _, a := range dryRun {
		if err := auth.ValidateActionString(a); err != nil {
			return nil, fmt.Errorf("--dry-run %q: %w", a, err)
		}
		if strings.HasSuffix(a, ":read") {
			return nil, fmt.Errorf("--dry-run %q: dry-run is meaningless for read actions", a)
		}
		if a == "auth:create" || a == "auth:revoke" || a == "auth:rotate" {
			return nil, fmt.Errorf("--dry-run %q: auth mutations are not dry-runnable", a)
		}
		out = append(out, auth.GrantEntry{Action: a, Mode: auth.ModeDryRun})
	}
	return out, nil
}

// authStatusResp is the shape returned by GET /auth/status.
type authStatusResp struct {
	Mode           string `json:"mode"`
	ActiveKeyCount int    `json:"active_key_count"`
	AdminCount     int    `json:"admin_count"`
}

// fetchAuthStatus issues GET /auth/status. Returns (response,
// reachable). reachable=false on transport failure (used by `auth
// init` for the soft "already authenticated?" check).
func fetchAuthStatus(ctx context.Context, endpoint, key string) (*authStatusResp, bool) {
	c := newAuthClient(endpoint, key)
	var resp authStatusResp
	if _, err := c.RawCall(ctx, "GET", "/auth/status", nil, &resp); err != nil {
		return nil, false
	}
	return &resp, true
}

// authQueryEscape escapes a glob filter for inclusion as a URL query value.
func authQueryEscape(s string) string { return url.QueryEscape(s) }

// bundledRoleNames returns the bundled role JSON names.
func bundledRoleNames() []string { return roles.AllNames() }
