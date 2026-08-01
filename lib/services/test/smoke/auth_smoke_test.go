// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestAuthSmoke_BootstrapLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ep := harness.BringUpRimsky(ctx, t)

	body := authGet(t, ep.BaseURL+"/v1/auth/status", "")
	if body["mode"] != "anonymous" {
		t.Fatalf("startup mode: got %v, want anonymous", body["mode"])
	}

	adminResp := authPost(t, ep.BaseURL+"/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminResp["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("admin key missing: %+v", adminResp)
	}

	body = authGet(t, ep.BaseURL+"/v1/auth/status", adminKey)
	if body["mode"] != "authenticated" {
		t.Fatalf("post-init mode: got %v, want authenticated", body["mode"])
	}

	roResp := authPost(t, ep.BaseURL+"/v1/auth/keys", adminKey, map[string]any{
		"name":        "smoke-readonly",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	roKey, _ := roResp["plaintext"].(string)
	if roKey == "" {
		t.Fatalf("ro key missing")
	}

	if code := authCode(t, "GET", ep.BaseURL+"/v1/auth/keys", roKey, nil); code != 200 {
		t.Fatalf("ro GET: %d", code)
	}
	if code := authCode(t, "DELETE", ep.BaseURL+"/v1/auth/keys/smoke-readonly", roKey, nil); code != 403 {
		t.Fatalf("ro DELETE: %d (want 403)", code)
	}

	rotResp := authPost(t, ep.BaseURL+"/v1/auth/keys/admin/rotate", adminKey, map[string]any{
		"grace": "1m",
	})
	newAdmin, _ := rotResp["plaintext"].(string)
	if newAdmin == "" || newAdmin == adminKey {
		t.Fatalf("rotated key empty or unchanged")
	}
	if code := authCode(t, "GET", ep.BaseURL+"/v1/auth/keys", adminKey, nil); code != 200 {
		t.Fatalf("old admin during grace: %d", code)
	}
	if code := authCode(t, "GET", ep.BaseURL+"/v1/auth/keys", newAdmin, nil); code != 200 {
		t.Fatalf("new admin: %d", code)
	}

	if code := authCode(t, "DELETE", ep.BaseURL+"/v1/auth/keys/smoke-readonly", newAdmin, nil); code != 200 {
		t.Fatalf("revoke ro: %d", code)
	}
	if code := authCode(t, "GET", ep.BaseURL+"/v1/auth/keys", roKey, nil); code == 200 {
		t.Fatalf("revoked ro key still authenticates: %d, want non-200 — revocation must actually stop the key from working", code)
	}
}

func authPost(t *testing.T, url, key string, body any) map[string]any {
	t.Helper()
	bs, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return doAndDecode(t, req)
}

func authGet(t *testing.T, url, key string) map[string]any {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return doAndDecode(t, req)
}

func authCode(t *testing.T, method, url, key string, body any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		bs, _ := json.Marshal(body)
		reader = bytes.NewReader(bs)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, url, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func doAndDecode(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", req.Method, req.URL.Path, err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: decode body: %v: %s", req.Method, req.URL.Path, err, string(raw))
		}
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("%s %s: %d %s", req.Method, req.URL.Path, resp.StatusCode, string(raw))
	}
	return out
}
