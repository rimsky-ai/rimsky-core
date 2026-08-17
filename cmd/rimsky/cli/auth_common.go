// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/roles"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func resolveAuthEndpointAndKey(flagEndpoint, flagKey string) (string, string, error) {
	cfgPath, _ := DefaultConfigPath()
	endpoint, err := ResolveEndpoint(flagEndpoint, os.Getenv("RIMSKY_CONTROL_API_URL"), cfgPath, "")
	if err != nil {
		return "", "", err
	}
	key := flagKey
	if key == "" {
		key = os.Getenv("RIMSKY_API_KEY")
	}
	if key == "" {
		key = ResolveAPIKeyFromContext(cfgPath)
	}
	return endpoint, key, nil
}

func newAuthClient(endpoint, key string) *Client {
	c := NewClient(endpoint)
	if key != "" {
		c.SetAPIKey(key)
	}
	return c
}

func formatAuthAPIError(method, path string, err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		body, _ := json.Marshal(apiErr.Body)
		return fmt.Errorf("%s %s: %d %s", method, path, apiErr.Status, strings.TrimSpace(string(body)))
	}
	return err
}

// @decision: auth-dry-run-request-flag
func reportAuthError(method, path string, err error) int {
	if code, ok := ReportDryRunPreview(err); ok {
		return code
	}
	fmt.Fprintln(os.Stderr, formatAuthAPIError(method, path, err))
	return 1
}

type roleSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Permissions auth.Grant `json:"permissions"`
}

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

func applyGrantPatches(grant auth.Grant, add, remove []string) (auth.Grant, error) {
	out := append(auth.Grant{}, grant...)

	for _, a := range add {
		if err := auth.ValidateActionString(a); err != nil {
			return nil, fmt.Errorf("--add %q: %w", a, err)
		}
		out = append(out, auth.GrantEntry{Action: a})
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

	return out, nil
}

type authStatusResp struct {
	Mode           string `json:"mode"`
	ActiveKeyCount int    `json:"active_key_count"`
	AdminCount     int    `json:"admin_count"`
}

func fetchAuthStatus(ctx context.Context, endpoint, key string) (*authStatusResp, error) {
	c := newAuthClient(endpoint, key)
	var resp authStatusResp
	if _, err := c.RawCall(ctx, "GET", "/v1/auth/status", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
