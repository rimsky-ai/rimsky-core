// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// authStringSliceFlag is a repeatable `--flag=value` accumulator used
// by the auth subcommands (--add / --remove / --dry-run).
type authStringSliceFlag []string

func (s *authStringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *authStringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// RunAuthCreateKey implements `rimsky auth create-key`.
func RunAuthCreateKey(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth create-key", flag.ContinueOnError)
	var (
		endpointFlag, keyFlag string
		name                  string
		roleName, rolePath    string
		expires               string
	)
	var addFlags, removeFlags, dryRunFlags authStringSliceFlag
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	fs.StringVar(&keyFlag, "key", "", "API key (Bearer token)")
	fs.StringVar(&name, "name", "", "name for the new key (required)")
	fs.StringVar(&roleName, "role", "", "bundled role name (required unless --role-file is set)")
	fs.StringVar(&rolePath, "role-file", "", "load role from a JSON file instead of bundled")
	fs.Var(&addFlags, "add", "append a grant entry with mode=execute (repeatable)")
	fs.Var(&removeFlags, "remove", "remove grant entries matching this action (repeatable)")
	fs.Var(&dryRunFlags, "dry-run", "append a grant entry with mode=dry_run (repeatable)")
	fs.StringVar(&expires, "expires", "", "duration until the key expires (e.g. 24h, 30d)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(name) == "" {
		fmt.Fprintln(os.Stderr, "rimsky auth create-key: --name is required")
		return 2
	}
	if roleName == "" && rolePath == "" {
		fmt.Fprintln(os.Stderr, "rimsky auth create-key: --role or --role-file is required")
		return 2
	}
	role, err := loadRole(roleName, rolePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	grant, err := applyGrantPatches(role.Permissions, addFlags, removeFlags, dryRunFlags)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	body := map[string]any{
		"name":        name,
		"permissions": grant,
	}
	if expires != "" {
		d, err := time.ParseDuration(expires)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid --expires duration:", err.Error())
			return 2
		}
		body["expires_at"] = time.Now().Add(d)
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := newAuthClient(endpoint, key)
	var resp struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Plaintext string `json:"plaintext"`
	}
	if _, err := c.RawCall(ctx, http.MethodPost, "/auth/keys", body, &resp); err != nil {
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodPost, "/auth/keys", err))
		return 1
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Save this API key now — it will not be shown again:")
	fmt.Fprintln(os.Stdout, "  "+resp.Plaintext)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "Hint: export RIMSKY_API_KEY=%q for subsequent commands.\n", resp.Plaintext)
	return 0
}
