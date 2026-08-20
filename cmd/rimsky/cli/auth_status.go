// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func RunAuthStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	var endpointFlag, keyFlag string
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	RegisterAPIKeyFlag(fs, &keyFlag)
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := newAuthClient(endpoint, key)
	var resp authStatusResp
	if _, err := c.RawCall(ctx, http.MethodGet, "/v1/auth/status", nil, &resp); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			fmt.Fprintln(os.Stderr, "rimsky auth status: auth required; set RIMSKY_API_KEY or pass --key")
			return 1
		}
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodGet, "/v1/auth/status", err))
		return 1
	}
	switch resp.Mode {
	case auth.StatusModeAnonymous:
		fmt.Fprintf(os.Stdout, "Mode: anonymous (%d keys provisioned)\n", resp.ActiveKeyCount)
	case auth.StatusModeAuthenticated:
		fmt.Fprintf(os.Stdout, "Mode: authenticated (%d keys total, %d admin)\n", resp.ActiveKeyCount, resp.AdminCount)
	default:
		fmt.Fprintf(os.Stdout, "Mode: %s (active=%d, admin=%d)\n", resp.Mode, resp.ActiveKeyCount, resp.AdminCount)
	}
	return 0
}
