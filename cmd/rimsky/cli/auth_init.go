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
)

// RunAuthInit bootstraps the deployment: mints an admin key by POSTing
// to /auth/keys against an anonymous-mode control-api. Refuses if any
// active key already exists.
func RunAuthInit(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth init", flag.ContinueOnError)
	var endpointFlag, keyFlag string
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	fs.StringVar(&keyFlag, "key", "", "API key (unused in anonymous mode)")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	// CLI-side nicety: refuse if the deployment is already
	// authenticated. The authoritative gate is the server's
	// anonymous-mode predicate; this is a friendly pre-check.
	if status, ok := fetchAuthStatus(ctx, endpoint, key); ok && status.Mode == "authenticated" {
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
		fmt.Fprintln(os.Stderr, "rimsky auth init: POST /auth/keys failed:", formatAuthAPIError(http.MethodPost, "/v1/auth/keys", err).Error())
		return 1
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Save this admin key now — it will not be shown again:")
	fmt.Fprintln(os.Stdout, "  "+resp.Plaintext)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Hint: export RIMSKY_API_KEY=\""+resp.Plaintext+"\" for subsequent commands.")
	return 0
}
