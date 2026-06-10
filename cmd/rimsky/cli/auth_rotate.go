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

// RunAuthRotate implements `rimsky auth rotate <name-or-id>`.
func RunAuthRotate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth rotate", flag.ContinueOnError)
	var (
		endpointFlag, keyFlag string
		grace                 string
	)
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	fs.StringVar(&keyFlag, "key", "", "API key (Bearer token)")
	fs.StringVar(&grace, "grace", "24h", "rotation grace duration")
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky auth rotate <name-or-id> [--grace=24h]")
		return 2
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	body := map[string]any{"grace": grace}
	path := "/v1/auth/keys/" + url.PathEscape(rest[0]) + "/rotate"
	c := newAuthClient(endpoint, key)
	var resp struct {
		OldKeyID  string `json:"old_key_id"`
		NewKeyID  string `json:"new_key_id"`
		Name      string `json:"name"`
		Plaintext string `json:"plaintext"`
		RevokeAt  string `json:"revoke_at"`
	}
	if _, err := c.RawCall(ctx, http.MethodPost, path, body, &resp); err != nil {
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodPost, path, err))
		return 1
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Save the new key plaintext now — it will not be shown again:")
	fmt.Fprintln(os.Stdout, "  "+resp.Plaintext)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "Old key revokes at %s.\n", resp.RevokeAt)
	return 0
}
