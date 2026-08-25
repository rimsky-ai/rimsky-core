// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
)

func servicePaginationDeps(t *testing.T) observability.Deps {
	t.Helper()
	d := newSQLiteDriver(t)
	return observability.Deps{
		Tables:    d.Tables(),
		Queue:     d.Queue(),
		Discovery: observability.NewDiscovery(&nopProber{}),
		Executors: []observability.ServiceSpec{
			{Name: "alpha", Endpoint: "alpha:9000"},
			{Name: "bravo", Endpoint: "bravo:9000"},
			{Name: "charlie", Endpoint: "charlie:9000"},
		},
		ClaimProducers: []observability.ServiceSpec{
			{Name: "delta", Endpoint: "delta:9000"},
			{Name: "echo", Endpoint: "echo:9000"},
			{Name: "foxtrot", Endpoint: "foxtrot:9000"},
		},
	}
}

func getObservabilityJSON(t *testing.T, r http.Handler, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	out := map[string]any{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v\n%s", path, err, w.Body.String())
		}
	}
	return w.Code, out
}

func TestServiceCollectionsPageThroughTheirCursor(t *testing.T) {
	r := newRouter(t, servicePaginationDeps(t))

	for _, tc := range []struct{ path, collection string }{
		{"/v1/observability/executors", "executors"},
		{"/v1/observability/claim-producers", "claim_producers"},
	} {
		t.Run(tc.collection, func(t *testing.T) {
			seen := map[string]bool{}
			cursor := ""
			pages := 0
			for {
				path := tc.path + "?limit=2"
				if cursor != "" {
					path += "&cursor=" + cursor
				}
				status, body := getObservabilityJSON(t, r, path)
				if status != http.StatusOK {
					t.Fatalf("GET %s = %d; body=%v", path, status, body)
				}
				rows, ok := body[tc.collection].([]any)
				if !ok {
					t.Fatalf("GET %s names its collection %q; body=%v", path, tc.collection, body)
				}
				if len(rows) > 2 {
					t.Fatalf("GET %s honored no limit: %d rows", path, len(rows))
				}
				for _, raw := range rows {
					seen[raw.(map[string]any)["name"].(string)] = true
				}
				if _, has := body["next_cursor"]; !has {
					t.Fatalf("next_cursor is present on every page; body=%v", body)
				}
				pages++
				if pages > 4 {
					t.Fatalf("the cursor walk did not terminate")
				}
				next, _ := body["next_cursor"].(string)
				if next == "" || len(rows) == 0 {
					break
				}
				cursor = next
			}
			if pages != 2 || len(seen) != 3 {
				t.Fatalf("three services at limit=2 come back over two pages; pages=%d seen=%v", pages, seen)
			}
		})
	}
}

func TestServiceCollectionsRefuseAMalformedLimit(t *testing.T) {
	r := newRouter(t, servicePaginationDeps(t))
	for _, path := range []string{
		"/v1/observability/executors",
		"/v1/observability/claim-producers",
	} {
		for _, bad := range []string{"limit=not-a-number", "limit=-1", "limit=0"} {
			status, body := getObservabilityJSON(t, r, path+"?"+bad)
			if status != http.StatusBadRequest {
				t.Errorf("GET %s?%s = %d, want 400; body=%v", path, bad, status, body)
			}
		}
	}
}
