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
	var (
		nameFilter     string
		includeRevoked bool
	)
	_, common, endpoint, code := runWithCommon("auth list", "[--name-filter <glob>] [--include-revoked]", HasTable, args,
		func(fs *flag.FlagSet) {
			fs.StringVar(&nameFilter, "name-filter", "", "glob filter on key name")
			fs.BoolVar(&includeRevoked, "include-revoked", false, "include revoked rows")
		})
	if common == nil {
		return code
	}
	c := newAuthClient(endpoint, common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	var failedPath string
	keys, err := PageAll(func(cursor string) ([]map[string]any, string, error) {
		q := url.Values{}
		if nameFilter != "" {
			q.Set("name_filter", nameFilter)
		}
		if includeRevoked {
			q.Set("include_revoked", "true")
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		path := "/v1/auth/keys"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		failedPath = path
		var resp struct {
			Keys       []map[string]any `json:"keys"`
			NextCursor string           `json:"next_cursor"`
		}
		if _, err := c.RawCall(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, "", err
		}
		return resp.Keys, resp.NextCursor, nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodGet, failedPath, err))
		return 1
	}
	return Render(common.Format, keys, func() {
		EmitTable(os.Stdout, []string{"NAME", "ID", "ROLE", "CREATED", "LAST_USED"}, authKeyRows(keys))
	})
}

func authKeyRows(keys []map[string]any) [][]string {
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
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
		rows = append(rows, []string{name, idShort, matchRole(perms), created, lastUsed})
	}
	return rows
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
