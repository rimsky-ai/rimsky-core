// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

// RunAuthRevoke implements `rimsky auth revoke <name-or-id>`.
func RunAuthRevoke(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth revoke", flag.ContinueOnError)
	var (
		endpointFlag, keyFlag string
		force                 bool
	)
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	fs.StringVar(&keyFlag, "key", "", "API key (Bearer token)")
	fs.BoolVar(&force, "force-leave-anonymous", false, "allow revocation that drops the deployment to zero active keys")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky auth revoke <name-or-id> [--force-leave-anonymous]")
		return 2
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	path := "/v1/auth/keys/" + url.PathEscape(rest[0])
	if force {
		path += "?force_leave_anonymous=true"
	}
	c := newAuthClient(endpoint, key)
	var resp map[string]any
	if _, err := c.RawCall(ctx, http.MethodDelete, path, nil, &resp); err != nil {
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodDelete, path, err))
		return 1
	}
	if name, _ := resp["name"].(string); name != "" {
		fmt.Fprintf(os.Stdout, "revoked key %q\n", name)
	} else {
		fmt.Fprintln(os.Stdout, "revoked")
	}
	return 0
}
