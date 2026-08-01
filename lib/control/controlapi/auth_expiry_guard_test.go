// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"net/http"
	"testing"
	"time"
)

func TestAuthCreateKey_FirstKeyWithExpiryRequiresForce(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	expires := h.clock.Now().Add(time.Hour)
	status, body := h.httpPostAs(t, "/v1/auth/keys", map[string]any{
		"name":        "first-key",
		"permissions": []map[string]any{{"action": "*"}},
		"expires_at":  expires.Format(time.RFC3339),
	}, "", "")
	if status != http.StatusConflict {
		t.Fatalf("creating the deployment's first key with an expiry without force: status=%d body=%v want 409", status, body)
	}

	status, body = h.httpPostAs(t, "/v1/auth/keys?force_expiring_key=true", map[string]any{
		"name":        "first-key",
		"permissions": []map[string]any{{"action": "*"}},
		"expires_at":  expires.Format(time.RFC3339),
	}, "", "")
	if status != http.StatusCreated {
		t.Fatalf("creating the first key with an expiry and force_expiring_key=true: status=%d body=%v want 201", status, body)
	}
}

func TestAuthCreateKey_FirstKeyWithoutExpiryDoesNotRequireForce(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	status, body := h.httpPostAs(t, "/v1/auth/keys", map[string]any{
		"name":        "permanent-key",
		"permissions": []map[string]any{{"action": "*"}},
	}, "", "")
	if status != http.StatusCreated {
		t.Fatalf("creating a permanent first key: status=%d body=%v want 201", status, body)
	}
}

func TestAuthRotateKey_SoleActiveExpiringKeyRequiresForce(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	adminKey, adminID := h.mintExpiringAPIKey(t, "sole-admin", []map[string]any{{"action": "*"}}, h.clock.Now().Add(time.Hour))

	status, body := h.httpPostAs(t, "/v1/auth/keys/"+adminID.String()+"/rotate", map[string]any{}, adminKey, "")
	if status != http.StatusConflict {
		t.Fatalf("rotating the sole active expiring key without force: status=%d body=%v want 409", status, body)
	}

	status, body = h.httpPostAs(t, "/v1/auth/keys/"+adminID.String()+"/rotate?force_expiring_key=true", map[string]any{}, adminKey, "")
	if status != http.StatusOK {
		t.Fatalf("rotating the sole active expiring key with force_expiring_key=true: status=%d body=%v want 200", status, body)
	}
}

func TestAuthRotateKey_NonSoleExpiringKeyDoesNotRequireForce(t *testing.T) {
	h := newSenderSubjectHarness(t)
	t.Cleanup(h.close)

	_, permanentID := h.mintActiveAPIKey(t, "permanent", []map[string]any{{"action": "*"}})
	expiringKey, expiringID := h.mintExpiringAPIKey(t, "expiring", []map[string]any{{"action": "*"}}, h.clock.Now().Add(time.Hour))
	_ = permanentID

	status, body := h.httpPostAs(t, "/v1/auth/keys/"+expiringID.String()+"/rotate", map[string]any{}, expiringKey, "")
	if status != http.StatusOK {
		t.Fatalf("rotating an expiring key while another active key exists: status=%d body=%v want 200", status, body)
	}
}
