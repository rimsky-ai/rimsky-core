// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func RunAuthStatus(ctx context.Context, args []string) int {
	_, common, endpoint, code := runWithCommon("auth status", "", NoTable, args, nil)
	if common == nil {
		return code
	}
	c := newAuthClient(endpoint, common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
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
	return Render(common.Format, resp, func() {
		switch resp.Mode {
		case auth.StatusModeAnonymous:
			fmt.Fprintf(os.Stdout, "Mode: anonymous (%d keys provisioned)\n", resp.ActiveKeyCount)
		case auth.StatusModeAuthenticated:
			fmt.Fprintf(os.Stdout, "Mode: authenticated (%d keys total, %d admin)\n", resp.ActiveKeyCount, resp.AdminCount)
		default:
			fmt.Fprintf(os.Stdout, "Mode: %s (active=%d, admin=%d)\n", resp.Mode, resp.ActiveKeyCount, resp.AdminCount)
		}
	})
}
