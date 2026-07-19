// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: permission
// @concept: asset

package auth_test

import (
	"net/http"
	"testing"
)

func TestAssetsRoute_RequiresReadAndDeleteGrants(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, adminBody := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := adminBody["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("mint admin: %+v", adminBody)
	}

	tplHash := seedDeployedTemplate(t, f, adminKey, "asset-authz")
	_, createResp := f.request(t, "POST", "/v1/instances", adminKey, map[string]any{
		"template": tplHash,
	})
	instanceID, _ := createResp["instance_id"].(string)
	if instanceID == "" {
		t.Fatalf("create instance: %+v", createResp)
	}
	assetsPath := "/v1/instances/" + instanceID + "/assets"

	if code, body := f.request(t, "GET", assetsPath, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("no-bearer assets list: got %d %+v; want 401", code, body)
	}

	_, narrowBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "no-assets",
		"permissions": []map[string]any{{"action": "instance:read"}},
	})
	narrowKey, _ := narrowBody["plaintext"].(string)
	if narrowKey == "" {
		t.Fatalf("mint narrow key: %+v", narrowBody)
	}
	if code, body := f.request(t, "GET", assetsPath, narrowKey, nil); code != http.StatusForbidden {
		t.Fatalf("wrong-grant assets list: got %d %+v; want 403 (instance:read must not imply asset:read)", code, body)
	}
	if code, body := f.request(t, "DELETE", assetsPath+"/producer.dataset", narrowKey, nil); code != http.StatusForbidden {
		t.Fatalf("wrong-grant asset delete: got %d %+v; want 403", code, body)
	}

	_, readerBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "asset-reader",
		"permissions": []map[string]any{{"action": "asset:read"}},
	})
	readerKey, _ := readerBody["plaintext"].(string)
	if readerKey == "" {
		t.Fatalf("mint asset-reader: %+v", readerBody)
	}
	code, listResp := f.request(t, "GET", assetsPath, readerKey, nil)
	if code != http.StatusOK {
		t.Fatalf("asset:read-granted assets list: got %d %+v; want 200", code, listResp)
	}
	items, _ := listResp["assets"].([]any)
	if len(items) != 0 {
		t.Fatalf("fresh instance with no committed durable claims: got %d assets; want 0", len(items))
	}
	if code, body := f.request(t, "DELETE", assetsPath+"/producer.dataset", readerKey, nil); code != http.StatusForbidden {
		t.Fatalf("asset:read-only key deleting an asset: got %d %+v; want 403 (asset:delete required, not granted by asset:read)",
			code, body)
	}

	_, deleterBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "asset-deleter",
		"permissions": []map[string]any{{"action": "asset:read"}, {"action": "asset:delete"}},
	})
	deleterKey, _ := deleterBody["plaintext"].(string)
	if deleterKey == "" {
		t.Fatalf("mint asset-deleter: %+v", deleterBody)
	}
	code, delResp := f.request(t, "DELETE", assetsPath+"/producer.dataset", deleterKey, nil)
	if code != http.StatusNotFound {
		t.Fatalf("asset:delete-granted delete of a nonexistent asset: got %d %+v; "+
			"want 404 (the permission gate must pass through to the handler, which reports not-found)", code, delResp)
	}
}
