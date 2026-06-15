// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Grant-scope enforcement end-to-end scenario per spec section
// "S-auth-grant-scope-enforced". A grant entry may scope a write action
// to a single resource selector (e.g. restrict `template:register` to
// tag "analytics"). The permission matcher must ACTUALLY enforce that
// scope: an in-scope request succeeds, an out-of-scope request of the
// same action is denied at the permission gate (HTTP 403 +
// `auth.access_denied` with denial_reason "permission_denied"), and the
// out-of-scope resource is never created.
//
// This is the RED proof for the scope wiring: grant scope is parsed and
// round-tripped into GrantEntry.Extras but never consulted by CheckGrant
// today, so a scoped `template:register` grant silently over-grants
// platform-wide register access for any tag. Today the out-of-scope
// "billing" register therefore SUCCEEDS (the request is the same spec, so
// it idempotently re-registers and upserts the billing tag onto the
// existing analytics row), and the 403 / no-billing-tag assertions FAIL —
// proving the test is coupled to the missing scope enforcement.
//
// @concept: permission

package auth_test

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// grantScopeSpec is the single-node TemplateSpec body the scope scenario
// registers under both the in-scope ("analytics") and out-of-scope
// ("billing") tags. The two registrations use the SAME spec so the
// out-of-scope attempt is provably distinguished from the in-scope one by
// scope alone (same action, same spec, only the tag differs) — the
// permission gate must reject it before persistence regardless of whether
// the underlying content-addressed row already exists.
func grantScopeSpec() map[string]any {
	return map[string]any{
		"name":    "grant-scope-seed",
		"version": "1",
		"nodes":   []map[string]any{{"type": "n1"}},
	}
}

// templateHasTag reports whether ANY template visible to the admin key
// carries the given tag. The scope scenario re-registers the SAME spec
// under "billing", which (absent scope enforcement) idempotently upserts
// the billing tag onto the existing analytics-tagged row rather than
// creating a fresh row — so a robust "no billing-tagged template" check
// scans every template's tags, not just for a separate row.
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

	// @deliberate: Mint admin via the anonymous path so we can mint scoped keys and
	// authoritatively list templates afterward.
	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("admin plaintext missing: %+v", body)
	}

	// @constraint: Mint a key whose grant scopes template:register to the single tag
	// "analytics" (least-privilege delegation) and otherwise grants reads.
	// The matcher must honor the scope: register-as-analytics succeeds,
	// register-as-billing is denied.
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

	// @constraint: In-scope: register a template tagged "analytics" with the scoped key.
	// The scope matches, so the write must succeed (201) and persist.
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

	// @deliberate: Admin sees the analytics-tagged template — the in-scope write landed.
	if !templateHasTag(t, f, adminKey, "analytics") {
		t.Fatalf("expected an analytics-tagged template after the in-scope register")
	}

	// @constraint: Out-of-scope: register the SAME spec tagged "billing" with the SAME
	// scoped key. The action matches but the scope ("analytics") does not
	// cover the "billing" target, so the permission gate must reject it
	// with HTTP 403 — denied on scope, not action.
	code, denyResp := f.request(t, "POST", "/v1/templates", scopedKey, map[string]any{
		"spec": grantScopeSpec(),
		"tag":  "billing",
	})
	if code != 403 {
		t.Fatalf("out-of-scope billing register: code=%d resp=%+v (want 403 — scope must deny the out-of-scope tag)", code, denyResp)
	}

	// @deliberate: The latest auth.access_denied audit row for template:register records
	// the scope rejection as denial_reason=permission_denied — the matcher
	// rejected it at the permission gate, not the action gate.
	f.flushAudit()
	ctx := context.Background()
	var sawScopeDenial bool
	if err := f.db.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := f.db.Tables().Events().List(ctx, persistence.EventListFilter{Kind: auth.EventAccessDenied}, persistence.ListPagination{Limit: 200}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			action, _ := e.Payload["action"].(string)
			if action != "template:register" {
				continue
			}
			if reason, _ := e.Payload["denial_reason"].(string); reason == string(auth.DenialPermissionDenied) {
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

	// @constraint: Persisted state confirms the out-of-scope resource was never created:
	// no template (new or existing) carries the "billing" tag. Absent scope
	// enforcement the same-spec re-register would have upserted billing onto
	// the existing analytics row, so this scan is what catches the leak.
	if templateHasTag(t, f, adminKey, "billing") {
		t.Fatalf("out-of-scope billing register persisted a billing-tagged template; scope was not enforced")
	}
}
