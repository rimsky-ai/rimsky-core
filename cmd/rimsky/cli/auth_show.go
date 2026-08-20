// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

func RunAuthShow(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("auth show", flag.ContinueOnError)
	var endpointFlag, keyFlag string
	fs.StringVar(&endpointFlag, "endpoint", "", "control-api endpoint URL")
	RegisterAPIKeyFlag(fs, &keyFlag)
	if err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky auth show <name-or-id>")
		return 2
	}
	endpoint, key, err := resolveAuthEndpointAndKey(endpointFlag, keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	path := "/v1/auth/keys/" + url.PathEscape(rest[0])
	c := newAuthClient(endpoint, key)
	var resp map[string]any
	if _, err := c.RawCall(ctx, http.MethodGet, path, nil, &resp); err != nil {
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodGet, path, err))
		return 1
	}
	bs, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Fprintln(os.Stdout, string(bs))
	return 0
}
