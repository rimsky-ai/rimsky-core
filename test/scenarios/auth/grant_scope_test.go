// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: permission

package auth_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func grantScopeSpec() map[string]any {
	return map[string]any{
		"name":    "grant-scope-seed",
		"version": "1",
		"nodes":   []map[string]any{{"type": "n1"}},
	}
}

func templateHasTag(t *testing.T, f *authFixture, adminKey, tag string) bool {
	t.Helper()
	code, resp := f.request(t, "GET", "/v1/templates", adminKey, nil)
	if code != 200 {
		t.Fatalf("GET /templates: %d %+v", code, resp)
	}
	list, _ := resp["templates"].([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		tags, _ := row["tags"].([]any)
		for _, tg := range tags {
			if s, _ := tg.(string); s == tag {
				return true
			}
		}
	}
	return false
}

func TestGrantScope_TemplateTagEnforced(t *testing.T) {
	f := newAuthFixture(t)
	defer f.Close()

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("admin plaintext missing: %+v", body)
	}

	_, scopedBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name": "analytics-only",
		"permissions": []map[string]any{
			{"action": "template:register", "scope": map[string]any{"template_tag": "analytics"}},
			{"action": "*:read"},
		},
	})
	scopedKey, _ := scopedBody["plaintext"].(string)
	if scopedKey == "" {
		t.Fatalf("scoped key plaintext missing: %+v", scopedBody)
	}

	code, regResp := f.request(t, "POST", "/v1/templates", scopedKey, map[string]any{
		"spec": grantScopeSpec(),
		"tag":  "analytics",
	})
	if code != 201 && code != 200 {
		t.Fatalf("in-scope analytics register: code=%d resp=%+v (want 201 — scope matches)", code, regResp)
	}
	hash, _ := regResp["template_id"].(string)
	if hash == "" {
		t.Fatalf("in-scope analytics register missing template_id: %+v", regResp)
	}

	if !templateHasTag(t, f, adminKey, "analytics") {
		t.Fatalf("expected an analytics-tagged template after the in-scope register")
	}

	code, denyResp := f.request(t, "POST", "/v1/templates", scopedKey, map[string]any{
		"spec": grantScopeSpec(),
		"tag":  "billing",
	})
	if code != 403 {
		t.Fatalf("out-of-scope billing register: code=%d resp=%+v (want 403 — scope must deny the out-of-scope tag)", code, denyResp)
	}

	ctx := context.Background()
	var sawScopeDenial bool
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{KindIn: []string{auth.EventAccessDenied}}, persistence.ListPagination{Limit: 200}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			action, _ := e.Payload.Map()["action"].(string)
			if action != "template:register" {
				continue
			}
			if reason, _ := e.Payload.Map()["denial_reason"].(string); reason == string(auth.DenialPermissionDenied) {
				sawScopeDenial = true
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if !sawScopeDenial {
		t.Fatalf("expected a template:register auth.access_denied row with denial_reason=permission_denied")
	}

	if templateHasTag(t, f, adminKey, "billing") {
		t.Fatalf("out-of-scope billing register persisted a billing-tagged template; scope was not enforced")
	}
}
