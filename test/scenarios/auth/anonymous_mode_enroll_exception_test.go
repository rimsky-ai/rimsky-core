// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: anonymous-mode-bootstrap

package auth_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
)

// @decision: enroll-token-is-api-key
func TestAnonymousModeOpensOperatorActionsButRefusesServiceEnrollment(t *testing.T) {
	ca, err := pki.GenerateCA(time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generate deployment CA: %v", err)
	}
	f := newAuthFixtureWithEnroll(t, &controlapi.EnrollDeps{CA: ca})
	defer f.Close()

	assertAuthStatus(t, f, "", "anonymous", 0, 0)

	for _, action := range []struct {
		name, method, path string
	}{
		{"list api keys", "GET", "/v1/auth/keys"},
		{"read auth status", "GET", "/v1/auth/status"},
		{"list templates", "GET", "/v1/templates"},
		{"list instances", "GET", "/v1/instances"},
	} {
		code, body := f.request(t, action.method, action.path, "", nil)
		if code != http.StatusOK {
			t.Errorf("anonymous %s (%s %s): got %d %+v, want 200 — every operator control-plane action succeeds on a fresh deployment",
				action.name, action.method, action.path, code, body)
		}
	}

	code, body := f.request(t, "POST", "/v1/enroll", "", map[string]any{"label": "a-service"})
	if code != http.StatusForbidden {
		t.Fatalf("anonymous POST /v1/enroll: got %d %+v, want 403 — machine service enrollment is the one deliberate exception to the open-on-first-run promise", code, body)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Errorf("the enrollment refusal must say why, got body: %+v", body)
	}
}
