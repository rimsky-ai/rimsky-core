// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func mintAPIKey(t *testing.T, ep harness.RimskyEndpoint, callerKey, name string, perms []map[string]any) string {
	t.Helper()
	headers := map[string]string{}
	if callerKey != "" {
		headers["Authorization"] = "Bearer " + callerKey
	}
	status, raw := ep.PostJSONWithHeaders(t, "/v1/auth/keys", map[string]any{
		"name":        name,
		"permissions": perms,
	}, headers)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /auth/keys (mint %q): %d %s", name, status, string(raw))
	}
	var resp struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode mint %q response: %v: %s", name, err, string(raw))
	}
	if resp.Plaintext == "" {
		t.Fatalf("mint %q: plaintext empty: %s", name, string(raw))
	}
	return resp.Plaintext
}

func deployScenarioTemplateAuth(t *testing.T, ep harness.RimskyEndpoint, bearer string, body map[string]any) string {
	t.Helper()
	authHeader := map[string]string{"Authorization": "Bearer " + bearer}
	status, raw := ep.PostJSONWithHeaders(t, "/v1/templates", body, authHeader)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSONWithHeaders(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{}, authHeader)
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

func tagListedAuth(t *testing.T, ep harness.RimskyEndpoint, bearer, name string) bool {
	t.Helper()
	cursor := ""
	for {
		path := "/v1/tags"
		if cursor != "" {
			path += "?cursor=" + cursor
		}
		status, raw := ep.GetJSON(t, path, bearer)
		if status != http.StatusOK {
			t.Fatalf("GET %s returned %d, want 200\nbody: %s", path, status, string(raw))
		}
		var resp struct {
			Tags []struct {
				Tag string `json:"tag"`
			} `json:"tags"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode GET %s response: %v\nbody: %s", path, err, string(raw))
		}
		for _, tg := range resp.Tags {
			if tg.Tag == name {
				return true
			}
		}
		if resp.NextCursor == "" {
			return false
		}
		cursor = resp.NextCursor
	}
}
