// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func deleteFromSenderSubjectHarness(t *testing.T, h *senderSubjectHarness, path, bearer string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, h.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return resp.StatusCode, out
}

func TestAuthRotateKey_RevokedKeyRefused409_ActiveKeySucceeds(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	adminKey, _ := h.mintActiveAPIKey(t, "admin", []map[string]any{{"action": "*"}})
	_, targetID := h.mintActiveAPIKey(t, "target", []map[string]any{{"action": "instance:read"}})

	rotateStatus, rotateBody := h.httpPostAs(t, "/v1/auth/keys/"+targetID.String()+"/rotate", map[string]any{}, adminKey, "")
	if rotateStatus != http.StatusOK {
		t.Fatalf("rotating an active key: got status=%d body=%v want 200 "+
			"(control: rotate must succeed on a non-revoked key)", rotateStatus, rotateBody)
	}

	_, secondTargetID := h.mintActiveAPIKey(t, "target2", []map[string]any{{"action": "instance:read"}})
	revokeStatus, revokeBody := deleteFromSenderSubjectHarness(t, h, "/v1/auth/keys/"+secondTargetID.String(), adminKey)
	if revokeStatus != http.StatusOK {
		t.Fatalf("revoking target2: status=%d body=%v", revokeStatus, revokeBody)
	}

	status, body := h.httpPostAs(t, "/v1/auth/keys/"+secondTargetID.String()+"/rotate", map[string]any{}, adminKey, "")
	if status != http.StatusConflict {
		t.Fatalf("rotating a revoked key: got status=%d body=%v want 409 "+
			"(auth_handlers.go::handleRotateKey must refuse rotation of an already-revoked key)", status, body)
	}
}
