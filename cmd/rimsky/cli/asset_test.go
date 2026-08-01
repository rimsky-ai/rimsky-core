// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestClient_ListAssets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances/abc/assets" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{
					"alias":          "loader.fs",
					"claim_id":       "claim-1",
					"producer_name":  "fs",
					"node_type":      "loader",
					"version_id":     "v1",
					"claimed_at":     time.Now().UTC().Format(time.RFC3339),
					"holder_node_id": "node-1",
					"state":          "committed",
					"lifetime":       "durable",
				},
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.ListAssets(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Assets) != 1 || resp.Assets[0].Alias != "loader.fs" {
		t.Errorf("assets: %+v", resp.Assets)
	}
	if resp.Assets[0].State != "committed" {
		t.Errorf("state: %q (want committed)", resp.Assets[0].State)
	}
	if resp.Assets[0].Lifetime != "durable" {
		t.Errorf("lifetime: %q (want durable)", resp.Assets[0].Lifetime)
	}
}

func TestClient_DeleteAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method: %s", r.Method)
		}
		if r.URL.Path != "/v1/instances/abc/assets/loader.fs" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.DeleteAsset(context.Background(), "abc", "loader.fs"); err != nil {
		t.Fatal(err)
	}
}

func TestClient_GetClaimAncestors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/lineage/claims/claim-1/ancestors" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("depth") != "5" {
			t.Errorf("depth missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ancestors": []map[string]any{
				{"id": "l1", "record_kind": "claim_terminal", "instance_id": "abc", "frame_id": "f", "record": map[string]any{}, "outcome": "committed"},
			},
			"depth": 5,
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.GetClaimAncestors(context.Background(), "claim-1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Ancestors) != 1 {
		t.Errorf("ancestors: %+v", resp.Ancestors)
	}
}

func TestRunAssetList_RequiresInstanceFlag(t *testing.T) {
	if code := RunAssetList(context.Background(), []string{"--endpoint", "http://unused.invalid"}); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestRunAssetList_ResolvesInstanceKeyThenRendersTable(t *testing.T) {
	var gotPath []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = append(gotPath, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/instances/my-key":
			_ = json.NewEncoder(w).Encode(map[string]any{"instance_id": "inst-uuid"})
		case "/v1/instances/inst-uuid/assets":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"assets": []map[string]any{
					{
						"alias": "loader.fs", "node_type": "loader", "producer_name": "fs",
						"version_id": "v1", "claimed_at": time.Now().UTC().Format(time.RFC3339),
						"state": "committed", "lifetime": "durable",
					},
				},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunAssetList(context.Background(), []string{"--endpoint", srv.URL, "--instance", "my-key"})
	})
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q", code, out)
	}
	if len(gotPath) != 2 || gotPath[0] != "/v1/instances/my-key" || gotPath[1] != "/v1/instances/inst-uuid/assets" {
		t.Fatalf("RunAssetList must resolve the instance key before listing assets on the resolved UUID; got %v", gotPath)
	}
	if !strings.Contains(out, "loader.fs") || !strings.Contains(out, "ALIAS") {
		t.Errorf("expected a rendered asset table; got %q", out)
	}
}

func TestRunAssetShow_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances/550e8400-e29b-41d4-a716-446655440001/assets/loader.fs.data" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"alias": "loader.fs.data", "claim_id": "claim-9", "producer_name": "fs",
			"node_type": "loader", "version_id": "v3",
			"claimed_at": time.Now().UTC().Format(time.RFC3339),
			"state":      "committed", "lifetime": "durable",
		})
	}))
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunAssetShow(context.Background(), []string{
			"--endpoint", srv.URL, "--instance", "550e8400-e29b-41d4-a716-446655440001", "--output", "json", "loader.fs.data",
		})
	})
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q", code, out)
	}
	var got AssetItem
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", out, err)
	}
	if got.ClaimID != "claim-9" || got.VersionID != "v3" {
		t.Errorf("decoded asset: %+v", got)
	}
}

func TestRunAssetShow_RequiresExactlyOneAlias(t *testing.T) {
	if code := RunAssetShow(context.Background(),
		[]string{"--endpoint", "http://unused.invalid", "--instance", "abc"}); code != 2 {
		t.Errorf("exit %d, want 2 with no alias arg", code)
	}
	if code := RunAssetShow(context.Background(),
		[]string{"--endpoint", "http://unused.invalid", "--instance", "abc", "a", "b"}); code != 2 {
		t.Errorf("exit %d, want 2 with two alias args", code)
	}
}

func TestRunAssetVersions_RendersTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances/550e8400-e29b-41d4-a716-446655440001/assets/loader.fs/versions" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"versions": []map[string]any{
				{"version_id": "v1", "committed_at_unix_s": 1767225600},
				{"version_id": "v2", "committed_at_unix_s": 1767312000},
			},
		})
	}))
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunAssetVersions(context.Background(), []string{
			"--endpoint", srv.URL, "--instance", "550e8400-e29b-41d4-a716-446655440001", "loader.fs",
		})
	})
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q", code, out)
	}
	if !strings.Contains(out, "v1") || !strings.Contains(out, "v2") || !strings.Contains(out, "VERSION_ID") {
		t.Errorf("expected a rendered versions table; got %q", out)
	}
	if !strings.Contains(out, "2026-01-01T00:00:00Z") || !strings.Contains(out, "2026-01-02T00:00:00Z") {
		t.Errorf("committed_at_unix_s should render as an RFC3339 COMMITTED_AT column, got %q", out)
	}
}

func TestRunAssetVersions_ServerErrorFieldExitsOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "alias not found"})
	}))
	defer srv.Close()

	code := RunAssetVersions(context.Background(), []string{
		"--endpoint", srv.URL, "--instance", "550e8400-e29b-41d4-a716-446655440001", "ghost.alias",
	})
	if code != 1 {
		t.Errorf("exit %d, want 1 when the response carries an error field", code)
	}
}

func TestRunAssetDelete_OK(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	}))
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunAssetDelete(context.Background(), []string{
			"--endpoint", srv.URL, "--instance", "550e8400-e29b-41d4-a716-446655440001", "loader.fs",
		})
	})
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q", code, out)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/instances/550e8400-e29b-41d4-a716-446655440001/assets/loader.fs" {
		t.Errorf("method/path: %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(out, "loader.fs") {
		t.Errorf("expected a confirmation naming the deleted alias; got %q", out)
	}
}

func TestRunAssetLineage_ChainsGetAssetThenGetClaimAncestorsAndFiltersByVersion(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/instances/550e8400-e29b-41d4-a716-446655440001/assets/loader.fs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"alias": "loader.fs", "claim_id": "claim-42", "producer_name": "fs",
				"claimed_at": time.Now().UTC().Format(time.RFC3339),
			})
		case "/v1/lineage/claims/claim-42/ancestors":
			if got := r.URL.Query().Get("depth"); got != "3" {
				t.Errorf("depth: %s, want default 3", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ancestors": []map[string]any{
					{"id": "l1", "record_kind": "claim_terminal", "record": map[string]any{"version_id": "v1"}},
					{"id": "l2", "record_kind": "claim_terminal", "record": map[string]any{"version_id": "v2"}},
				},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunAssetLineage(context.Background(), []string{
			"--endpoint", srv.URL, "--instance", "550e8400-e29b-41d4-a716-446655440001", "--version", "v2", "--output", "json", "loader.fs",
		})
	})
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q", code, out)
	}
	if len(gotPaths) != 2 || gotPaths[0] != "/v1/instances/550e8400-e29b-41d4-a716-446655440001/assets/loader.fs" ||
		gotPaths[1] != "/v1/lineage/claims/claim-42/ancestors" {
		t.Fatalf("RunAssetLineage must call GetAsset before GetClaimAncestors, using the resolved claim_id; got %v", gotPaths)
	}
	var rows []LineageRecordItem
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", out, err)
	}
	if len(rows) != 1 || rows[0].ID != "l2" {
		t.Fatalf("--version v2 must filter out non-matching lineage rows; got %+v", rows)
	}
}

func TestResolveInstanceUUID_PassesUUIDThroughWithoutARequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	const uuid = "550e8400-e29b-41d4-a716-446655440000"
	got, err := resolveInstanceUUID(context.Background(), c, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if got != uuid {
		t.Errorf("got %q, want passthrough of %q", got, uuid)
	}
	if called {
		t.Error("a UUID-shaped ref must not trigger a GetInstance lookup")
	}
}

func TestResolveInstanceUUID_ResolvesAliasViaGetInstance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances/my-alias" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"instance_id": "resolved-uuid"})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := resolveInstanceUUID(context.Background(), c, "my-alias")
	if err != nil {
		t.Fatal(err)
	}
	if got != "resolved-uuid" {
		t.Errorf("got %q, want resolved-uuid", got)
	}
}

func TestRecordCarriesVersion(t *testing.T) {
	cases := []struct {
		name   string
		record string
		want   string
		expect bool
	}{
		{"match", `{"version_id":"v1"}`, "v1", true},
		{"mismatch", `{"version_id":"v1"}`, "v2", false},
		{"empty_want_never_matches", `{"version_id":"v1"}`, "", false},
		{"empty_record", ``, "v1", false},
		{"malformed_json", `not-json`, "v1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := recordCarriesVersion(json.RawMessage(c.record), c.want)
			if got != c.expect {
				t.Errorf("recordCarriesVersion(%q, %q) = %t, want %t", c.record, c.want, got, c.expect)
			}
		})
	}
}

func TestTruncateSnippet(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under_limit_unchanged", "short", 60, "short"},
		{"exactly_at_limit_unchanged", "12345", 5, "12345"},
		{"over_limit_truncated_with_ellipsis", "123456789", 5, "1234…"},
		{"newlines_collapsed_to_spaces", "a\nb\nc", 60, "a b c"},
		{"multibyte_rune_boundary_not_split", "日本語のテキスト", 5, "日本語の…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncateSnippet(c.in, c.max)
			if got != c.want {
				t.Errorf("truncateSnippet(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncateSnippet(%q, %d) = %q is not valid UTF-8", c.in, c.max, got)
			}
		})
	}
}
