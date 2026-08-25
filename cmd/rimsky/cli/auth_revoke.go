// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

func RunAuthRevoke(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth revoke", flag.ContinueOnError)
	SetUsage(fs, UsageLine("auth revoke", "<name-or-id> [--force-leave-anonymous] [--yes]"))
	var (
		endpointFlag, keyFlag string
		force                 bool
		yes                   bool
	)
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	RegisterAPIKeyFlag(fs, &keyFlag)
	fs.BoolVar(&force, "force-leave-anonymous", false, "allow revocation that drops the deployment to zero active keys")
	RegisterYesFlag(fs, &yes)
	if code, done := ParseVerbFlags(fs, args); done {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	if !ConfirmDestructiveTargets(yes, "revoke api-key "+rest[0]) {
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
		return reportAuthError(http.MethodDelete, path, err)
	}
	if name, _ := resp["name"].(string); name != "" {
		fmt.Fprintf(os.Stdout, "revoked key %q\n", name)
	} else {
		fmt.Fprintln(os.Stdout, "revoked")
	}
	return 0
}
