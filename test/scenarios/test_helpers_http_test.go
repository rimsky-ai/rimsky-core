// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Small HTTP helpers shared by scenario tests. Kept in a `_test.go`
// build tag implicitly via the package-level package declaration so
// the helpers don't ship in the non-test build.

package scenarios

import (
	"io"
	"net/http"
	"testing"
)

// httpGetJSON issues a GET, reads the response body, and returns the
// status code + body bytes. Fatals on transport / read errors.
func httpGetJSON(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", url, err)
	}
	return resp.StatusCode, b
}
