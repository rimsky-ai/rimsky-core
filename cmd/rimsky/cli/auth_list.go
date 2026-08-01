// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/roles"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func RunAuthList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth list", flag.ContinueOnError)
	var (
		endpointFlag, keyFlag string
		nameFilter            string
		includeRevoked        bool
		jsonOut               bool
	)
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	fs.StringVar(&keyFlag, "key", "", "API key (Bearer token)")
	fs.StringVar(&nameFilter, "name-filter", "", "glob filter on key name")
	fs.BoolVar(&includeRevoked, "include-revoked", false, "include revoked rows")
	fs.BoolVar(&jsonOut, "json", false, "output JSON")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	path := "/v1/auth/keys"
	q := url.Values{}
	if nameFilter != "" {
		q.Set("name_filter", nameFilter)
	}
	if includeRevoked {
		q.Set("include_revoked", "true")
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	c := newAuthClient(endpoint, key)
	var resp struct {
		Keys []map[string]any `json:"keys"`
	}
	if _, err := c.RawCall(ctx, http.MethodGet, path, nil, &resp); err != nil {
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodGet, path, err))
		return 1
	}
	if jsonOut {
		bs, _ := json.MarshalIndent(resp.Keys, "", "  ")
		fmt.Fprintln(os.Stdout, string(bs))
		return 0
	}
	if len(resp.Keys) == 0 {
		fmt.Fprintln(os.Stdout, "(no API keys)")
		return 0
	}
	fmt.Fprintln(os.Stdout, "NAME\tID\tROLE\tCREATED\tLAST_USED")
	for _, k := range resp.Keys {
		name, _ := k["name"].(string)
		id, _ := k["id"].(string)
		created, _ := k["created_at"].(string)
		lastUsed, _ := k["last_used_at"].(string)
		idShort := id
		if len(id) > 8 {
			idShort = id[:8]
		}
		permsRaw, _ := json.Marshal(k["permissions"])
		var perms auth.Grant
		_ = json.Unmarshal(permsRaw, &perms)
		roleMatch := matchRole(perms)
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\n", name, idShort, roleMatch, created, lastUsed)
	}
	return 0
}

func matchRole(g auth.Grant) string {
	for _, name := range roles.AllNames() {
		r, err := loadRole(name, "")
		if err != nil {
			continue
		}
		if grantsEqual(g, r.Permissions) {
			return "role:" + name
		}
	}
	return "custom"
}

func grantsEqual(a, b auth.Grant) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, e := range a {
		counts[grantEntryKey(e)]++
	}
	for _, e := range b {
		key := grantEntryKey(e)
		counts[key]--
		if counts[key] < 0 {
			return false
		}
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

func grantEntryKey(e auth.GrantEntry) string {
	scopeKeys := make([]string, 0, len(e.Scope))
	for k := range e.Scope {
		scopeKeys = append(scopeKeys, k)
	}
	sort.Strings(scopeKeys)
	var b strings.Builder
	b.WriteString(e.Action)
	b.WriteByte('\x00')
	b.WriteString(string(e.Mode))
	for _, k := range scopeKeys {
		b.WriteByte('\x00')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(e.Scope[k])
	}
	return b.String()
}
