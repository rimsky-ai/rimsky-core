// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

// RunAuthList implements `rimsky auth list`.
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	path := "/auth/keys"
	q := ""
	if nameFilter != "" {
		q += "name_filter=" + authQueryEscape(nameFilter)
	}
	if includeRevoked {
		if q != "" {
			q += "&"
		}
		q += "include_revoked=true"
	}
	if q != "" {
		path += "?" + q
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

// matchRole renders a human label for the grant: "role:<name>" when
// the grant matches a bundled role's expansion exactly; otherwise
// "custom".
func matchRole(g auth.Grant) string {
	for _, name := range bundledRoleNames() {
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
	for i := range a {
		if a[i].Action != b[i].Action || a[i].Mode != b[i].Mode {
			return false
		}
	}
	return true
}
