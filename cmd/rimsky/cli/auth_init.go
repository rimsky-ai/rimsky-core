// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

func RunAuthInit(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth init", flag.ContinueOnError)
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

	if status, err := fetchAuthStatus(ctx, endpoint, key); err == nil && status.Mode == auth.StatusModeAuthenticated {
		fmt.Fprintln(os.Stderr, "rimsky auth init: deployment is already authenticated (use 'rimsky auth create-key' instead)")
		return 1
	}

	role, err := loadRole("admin", "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	body := map[string]any{
		"name":        "admin",
		"permissions": role.Permissions,
	}
	c := newAuthClient(endpoint, "")
	var resp struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Plaintext string `json:"plaintext"`
	}
	if _, err := c.RawCall(ctx, http.MethodPost, "/v1/auth/keys", body, &resp); err != nil {
		return reportAuthError(http.MethodPost, "/v1/auth/keys", err)
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Save this admin key now — it will not be shown again:")
	fmt.Fprintln(os.Stdout, "  "+resp.Plaintext)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Hint: export RIMSKY_API_KEY=\""+resp.Plaintext+"\" for subsequent commands.")
	return 0
}
