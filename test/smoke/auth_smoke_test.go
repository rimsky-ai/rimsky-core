// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Auth-smoke: end-to-end exercise of the auth-key lifecycle against
// the real SmokeStack (postgres-backed control-api). Verifies the
// bootstrap → mint → rotate → revoke flow plus the implicit
// anonymous-mode floor.

package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestAuthSmoke_BootstrapLifecycle(t *testing.T) {
	stack := BringUpStack(t)

	// 1. Anonymous mode at startup.
	body := authGetJSON(t, stack.ControlBase+"/auth/status", "")
	if body["mode"] != "anonymous" {
		t.Fatalf("startup mode: got %v, want anonymous", body["mode"])
	}

	// 2. Mint admin via anonymous mode (no Bearer).
	adminResp := authPostJSON(t, stack.ControlBase+"/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminResp["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("admin key missing: %+v", adminResp)
	}

	// 3. Authenticated now.
	body = authGetJSON(t, stack.ControlBase+"/auth/status", adminKey)
	if body["mode"] != "authenticated" {
		t.Fatalf("post-init mode: got %v, want authenticated", body["mode"])
	}

	// 4. Mint a read-only key with the admin token.
	roResp := authPostJSON(t, stack.ControlBase+"/auth/keys", adminKey, map[string]any{
		"name":        "smoke-readonly",
		"permissions": []map[string]any{{"action": "*:read"}},
	})
	roKey, _ := roResp["plaintext"].(string)
	if roKey == "" {
		t.Fatalf("ro key missing")
	}

	// 5. Read-only key can GET /auth/keys but cannot DELETE.
	if code := authStatusCode(t, "GET", stack.ControlBase+"/auth/keys", roKey, nil); code != 200 {
		t.Fatalf("ro GET: %d", code)
	}
	if code := authStatusCode(t, "DELETE", stack.ControlBase+"/auth/keys/smoke-readonly", roKey, nil); code != 403 {
		t.Fatalf("ro DELETE: %d (want 403)", code)
	}

	// 6. Rotate admin with 1m grace; both keys work.
	rotResp := authPostJSON(t, stack.ControlBase+"/auth/keys/admin/rotate", adminKey, map[string]any{
		"grace": "1m",
	})
	newAdmin, _ := rotResp["plaintext"].(string)
	if newAdmin == "" || newAdmin == adminKey {
		t.Fatalf("rotated key empty or unchanged")
	}
	if code := authStatusCode(t, "GET", stack.ControlBase+"/auth/keys", adminKey, nil); code != 200 {
		t.Fatalf("old admin during grace: %d", code)
	}
	if code := authStatusCode(t, "GET", stack.ControlBase+"/auth/keys", newAdmin, nil); code != 200 {
		t.Fatalf("new admin: %d", code)
	}

	// 7. Revoke read-only.
	if code := authStatusCode(t, "DELETE", stack.ControlBase+"/auth/keys/smoke-readonly", newAdmin, nil); code != 200 {
		t.Fatalf("revoke ro: %d", code)
	}
}

// helpers ------------------------------------------------------------

func authPostJSON(t *testing.T, url, key string, body any) map[string]any {
	t.Helper()
	bs, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return doAndDecode(t, req)
}

func authGetJSON(t *testing.T, url, key string) map[string]any {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return doAndDecode(t, req)
}

func authStatusCode(t *testing.T, method, url, key string, body any) int {
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
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("%s %s: %d %s", req.Method, req.URL.Path, resp.StatusCode, string(raw))
	}
	return out
}
