// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
)

// RunAuthStatus implements `rimsky auth status`. Tolerates a missing
// API key: in anonymous mode the server admits unauthenticated
// requests (synthetic admin); in authenticated mode the call returns
// 401 and the CLI surfaces that.
func RunAuthStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	var endpointFlag, keyFlag string
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	fs.StringVar(&keyFlag, "key", "", "API key (Bearer token)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := newAuthClient(endpoint, key)
	var resp authStatusResp
	if _, err := c.RawCall(ctx, http.MethodGet, "/auth/status", nil, &resp); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			fmt.Fprintln(os.Stderr, "rimsky auth status: auth required; set RIMSKY_API_KEY or pass --key")
			return 1
		}
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodGet, "/auth/status", err))
		return 1
	}
	switch resp.Mode {
	case "anonymous":
		fmt.Fprintf(os.Stdout, "Mode: anonymous (%d keys provisioned)\n", resp.ActiveKeyCount)
	case "authenticated":
		fmt.Fprintf(os.Stdout, "Mode: authenticated (%d keys total, %d admin)\n", resp.ActiveKeyCount, resp.AdminCount)
	default:
		fmt.Fprintf(os.Stdout, "Mode: %s (active=%d, admin=%d)\n", resp.Mode, resp.ActiveKeyCount, resp.AdminCount)
	}
	return 0
}
