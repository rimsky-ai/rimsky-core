// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
)

func RunAuthShow(ctx context.Context, args []string) int {
	fs, common, endpoint, code := runWithCommon("auth show", "<name-or-id>", NoTable, args, nil)
	if common == nil {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return UsageError(fs)
	}
	path := "/v1/auth/keys/" + url.PathEscape(rest[0])
	c := newAuthClient(endpoint, common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))
	var resp map[string]any
	if _, err := c.RawCall(ctx, http.MethodGet, path, nil, &resp); err != nil {
		fmt.Fprintln(os.Stderr, formatAuthAPIError(http.MethodGet, path, err))
		return 1
	}
	return Render(common.Format, resp, func() {
		EmitKV(os.Stdout, sortedFieldPairs(resp))
	})
}

func sortedFieldPairs(fields map[string]any) [][2]string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([][2]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, [2]string{name, scalarOrJSON(fields[name])})
	}
	return pairs
}

func scalarOrJSON(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return fmt.Sprintf("%t", typed)
	case float64:
		return fmt.Sprintf("%v", typed)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}
