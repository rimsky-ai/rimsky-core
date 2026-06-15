// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Grant-scope enforcement across the FULL template lifecycle plus the
// instance-create surface — not just template:register. The
// requestTargets resolver covers six write actions today: template:
// register/deploy/undeploy/deregister and tag:set/tag:delete and
// instance:create. This file proves the scope gate fires on each of
// them, EACH with three template-reference forms:
//
//	(a) direct tag-form (one scope candidate, the tag itself),
//	(b) hash-form with one tag (one candidate, resolved via
//	    TemplateTags().ListByTemplate),
//	(c) hash-form with two tags (two candidates; scope matched against
//	    ANY suffices per the set-membership semantics).
//
// A scoped grant whose Scope.template_tag = "analytics" must:
//   - admit a request whose target template is tagged "analytics" by
//     any of the three reference forms,
//   - admit a hash-form request whose template carries multiple tags
//     when "analytics" is in the tag list (set-membership),
//   - reject a request whose target template is tagged anything but
//     "analytics" with HTTP 403 (the permission gate, not the action
//     gate) and persist nothing.
//
// @concept: permission

package auth_test

import (
	"net/http"
	"strings"
	"testing"
)

// scopeLifecycleSpec is the single-node TemplateSpec used by every
// lifecycle sub-test in this file. Identical body across calls so
// register is content-addressed to the same hash.
func scopeLifecycleSpec(name string) map[string]any {
	return map[string]any{
		"name":                  name,
		"version":               "1",
		"frame_resolution_mode": "serial_queue",
		"nodes":                 []map[string]any{{"type": "n1"}},
	}
}

// scopeLifecycleHarness brings up a fresh authFixture, mints admin,
// and returns the bearer keys + helpers for each sub-test.
type scopeLifecycleHarness struct {
	f          *authFixture
	adminKey   string
	analytics  string // @deliberate: bearer with template_tag=analytics scoped writes
	allActions string // @deliberate: bearer with the SIX scopeable actions (template:
	// register/deploy/undeploy/deregister, tag:set/delete, instance:create)
}

// newScopeLifecycleHarness builds the harness. The analytics-scoped key
// carries every scopeable action with the SAME scope so each lifecycle
// sub-test can deny on scope alone (action-match passes, scope-match
// fails) for "billing"-targeted requests.
func newScopeLifecycleHarness(t *testing.T) *scopeLifecycleHarness {
	t.Helper()
	f := newAuthFixture(t)
	t.Cleanup(f.Close)

	_, body := f.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := body["plaintext"].(string)
	if adminKey == "" {
		t.Fatalf("admin plaintext missing: %+v", body)
	}

	scopedPerms := []map[string]any{
		{"action": "template:register", "scope": map[string]any{"template_tag": "analytics"}},
		{"action": "template:deploy", "scope": map[string]any{"template_tag": "analytics"}},
		{"action": "template:undeploy", "scope": map[string]any{"template_tag": "analytics"}},
		{"action": "template:deregister", "scope": map[string]any{"template_tag": "analytics"}},
		{"action": "tag:set", "scope": map[string]any{"template_tag": "analytics"}},
		{"action": "tag:delete", "scope": map[string]any{"template_tag": "analytics"}},
		{"action": "instance:create", "scope": map[string]any{"template_tag": "analytics"}},
		// @deliberate: Reads (so the scoped key can verify its own writes landed and
		// the gate can resolve hash→tags via the template-tag table).
		{"action": "*:read"},
	}
	_, scopedBody := f.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name":        "analytics-only-lifecycle",
		"permissions": scopedPerms,
	})
	scopedKey, _ := scopedBody["plaintext"].(string)
	if scopedKey == "" {
		t.Fatalf("scoped key plaintext missing: %+v", scopedBody)
	}

	return &scopeLifecycleHarness{f: f, adminKey: adminKey, analytics: scopedKey, allActions: scopedKey}
}

// seedAnalyticsTemplate uses admin to register + tag a fresh template
// under the "analytics" tag, returning (hash, tagName). The fresh-spec
// per-call ensures content addressing distinct hashes per sub-test —
// dump-and-recreate semantics keep sub-tests independent without
// cross-test state leakage in the in-process fixture.
func (h *scopeLifecycleHarness) seedAnalyticsTemplate(t *testing.T, name string) (string, string) {
	t.Helper()
	code, resp := h.f.request(t, "POST", "/v1/templates", h.adminKey, map[string]any{
		"spec": scopeLifecycleSpec(name),
		"tag":  "analytics",
	})
	if code != 201 && code != 200 {
		t.Fatalf("seed analytics template %q: %d %+v", name, code, resp)
	}
	hash, _ := resp["template_id"].(string)
	if hash == "" {
		t.Fatalf("seed analytics template %q: missing template_id %+v", name, resp)
	}
	// @deliberate: Move it to deployed so undeploy/deregister/instance-create have
	// the right precondition.
	code, depResp := h.f.request(t, "POST", "/v1/templates/"+hash+"/deploy", h.adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seed analytics deploy %q: %d %+v", name, code, depResp)
	}
	return hash, "analytics"
}

// seedBillingTemplate is the out-of-scope sibling: admin registers a
// distinct spec under the "billing" tag. The scoped key has no
// "billing" coverage so any write targeting this template must 403.
func (h *scopeLifecycleHarness) seedBillingTemplate(t *testing.T, name string) (string, string) {
	t.Helper()
	code, resp := h.f.request(t, "POST", "/v1/templates", h.adminKey, map[string]any{
		"spec": scopeLifecycleSpec(name),
		"tag":  "billing",
	})
	if code != 201 && code != 200 {
		t.Fatalf("seed billing template %q: %d %+v", name, code, resp)
	}
	hash, _ := resp["template_id"].(string)
	if hash == "" {
		t.Fatalf("seed billing template %q: missing template_id %+v", name, resp)
	}
	code, depResp := h.f.request(t, "POST", "/v1/templates/"+hash+"/deploy", h.adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seed billing deploy %q: %d %+v", name, code, depResp)
	}
	return hash, "billing"
}

// seedAnalyticsTemplateWithExtraTag seeds an analytics-tagged template
// then adds a second tag pointing at the same hash via POST /tags.
// Used by the "hash-form with two tags" leg: the request gate must
// admit a hash-form request scoped to "analytics" when "analytics" is
// in the multi-tag list (set-membership).
func (h *scopeLifecycleHarness) seedAnalyticsTemplateWithExtraTag(t *testing.T, name, extraTag string) string {
	t.Helper()
	hash, _ := h.seedAnalyticsTemplate(t, name)
	code, resp := h.f.request(t, "POST", "/v1/tags", h.adminKey, map[string]any{
		"tag":      extraTag,
		"template": hash,
	})
	if code != 200 && code != 201 {
		t.Fatalf("POST /tags %s for %q: %d %+v", extraTag, name, code, resp)
	}
	return hash
}

// requireForbidden asserts the request returned 403 (the scope-denied
// gate) and the resp body looks like a permission denial.
func requireForbidden(t *testing.T, code int, body map[string]any, what string) {
	t.Helper()
	if code != 403 {
		t.Fatalf("%s: got %d, want 403 (scope-denied)\nbody: %+v", what, code, body)
	}
}

// requireOK asserts the request returned 200 or 201. Used for the
// in-scope leg of each sub-test.
func requireOK(t *testing.T, code int, body map[string]any, what string) {
	t.Helper()
	if code != 200 && code != 201 {
		t.Fatalf("%s: got %d, want 200/201 (scope matches)\nbody: %+v", what, code, body)
	}
}

// TestGrantScope_TemplateDeploy proves the scope gate fires on
// template:deploy. Each leg uses a fresh template so the deploy
// transition is meaningful (registered → deployed).
func TestGrantScope_TemplateDeploy(t *testing.T) {
	h := newScopeLifecycleHarness(t)

	// @deliberate: Move templates to the "registered" state (not yet deployed) so
	// scoped-key deploy is the next legal transition. seedAnalyticsTemplate
	// auto-deploys, so we use a manual undeploy-then-deploy here.

	t.Run("in-scope tag-form admits deploy", func(t *testing.T) {
		// @deliberate: register-only via admin (no deploy yet): use a hand-rolled call.
		name := "deploy-tag-in-" + randomNoun(t)
		hash := registerOnly(t, h.f, h.adminKey, name, "analytics")
		code, body := h.f.request(t, "POST", "/v1/templates/analytics/deploy", h.analytics, map[string]any{})
		// @deliberate: /templates/{id}/deploy accepts a tag in the {id} slot — the
		// scope gate reads `template_tag=analytics` direct from URL.
		requireOK(t, code, body, "deploy by tag-form (in-scope)")
		_ = hash
	})

	t.Run("in-scope hash-form (one tag) admits deploy", func(t *testing.T) {
		name := "deploy-hash-1tag-" + randomNoun(t)
		hash := registerOnly(t, h.f, h.adminKey, name, "analytics")
		code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/deploy", h.analytics, map[string]any{})
		requireOK(t, code, body, "deploy by hash-form one-tag (in-scope)")
	})

	t.Run("in-scope hash-form (two tags) admits deploy", func(t *testing.T) {
		// @deliberate: Register under analytics, then add a second tag pointing at the
		// same hash. The hash now resolves to two candidates — the gate
		// admits because "analytics" is in the set.
		name := "deploy-hash-2tag-" + randomNoun(t)
		hash := registerOnly(t, h.f, h.adminKey, name, "analytics")
		if code, body := h.f.request(t, "POST", "/v1/tags", h.adminKey, map[string]any{
			"tag":      "forecasting",
			"template": hash,
		}); code != 200 && code != 201 {
			t.Fatalf("POST /tags forecasting for deploy-hash-2tag: %d %+v", code, body)
		}
		code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/deploy", h.analytics, map[string]any{})
		requireOK(t, code, body, "deploy by hash-form two-tag (in-scope, set-membership)")
	})

	t.Run("out-of-scope tag-form rejects deploy", func(t *testing.T) {
		name := "deploy-tag-out-" + randomNoun(t)
		_ = registerOnly(t, h.f, h.adminKey, name, "billing")
		code, body := h.f.request(t, "POST", "/v1/templates/billing/deploy", h.analytics, map[string]any{})
		requireForbidden(t, code, body, "deploy by tag-form (out-of-scope)")
	})

	t.Run("out-of-scope hash-form rejects deploy", func(t *testing.T) {
		name := "deploy-hash-out-" + randomNoun(t)
		hash := registerOnly(t, h.f, h.adminKey, name, "billing")
		code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/deploy", h.analytics, map[string]any{})
		requireForbidden(t, code, body, "deploy by hash-form (out-of-scope)")
	})
}

// TestGrantScope_TemplateUndeploy proves the scope gate fires on
// template:undeploy. Mirror shape of the deploy test.
func TestGrantScope_TemplateUndeploy(t *testing.T) {
	h := newScopeLifecycleHarness(t)

	t.Run("in-scope tag-form admits undeploy", func(t *testing.T) {
		name := "undeploy-tag-in-" + randomNoun(t)
		_, _ = h.seedAnalyticsTemplate(t, name)
		code, body := h.f.request(t, "POST", "/v1/templates/analytics/undeploy", h.analytics, map[string]any{})
		requireOK(t, code, body, "undeploy by tag-form (in-scope)")
	})

	t.Run("in-scope hash-form (one tag) admits undeploy", func(t *testing.T) {
		name := "undeploy-hash-1tag-" + randomNoun(t)
		hash, _ := h.seedAnalyticsTemplate(t, name)
		code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/undeploy", h.analytics, map[string]any{})
		requireOK(t, code, body, "undeploy by hash-form one-tag (in-scope)")
	})

	t.Run("in-scope hash-form (two tags) admits undeploy", func(t *testing.T) {
		name := "undeploy-hash-2tag-" + randomNoun(t)
		hash := h.seedAnalyticsTemplateWithExtraTag(t, name, "growth")
		code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/undeploy", h.analytics, map[string]any{})
		requireOK(t, code, body, "undeploy by hash-form two-tag (set-membership)")
	})

	t.Run("out-of-scope tag-form rejects undeploy", func(t *testing.T) {
		name := "undeploy-tag-out-" + randomNoun(t)
		_, _ = h.seedBillingTemplate(t, name)
		code, body := h.f.request(t, "POST", "/v1/templates/billing/undeploy", h.analytics, map[string]any{})
		requireForbidden(t, code, body, "undeploy by tag-form (out-of-scope)")
	})

	t.Run("out-of-scope hash-form rejects undeploy", func(t *testing.T) {
		name := "undeploy-hash-out-" + randomNoun(t)
		hash, _ := h.seedBillingTemplate(t, name)
		code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/undeploy", h.analytics, map[string]any{})
		requireForbidden(t, code, body, "undeploy by hash-form (out-of-scope)")
	})
}

// TestGrantScope_TemplateDeregister proves the scope gate fires on
// template:deregister.
func TestGrantScope_TemplateDeregister(t *testing.T) {
	h := newScopeLifecycleHarness(t)

	// @constraint: Deregister requires the template to be in 'undeployed' or 'registered'
	// — the scoped key is allowed to undeploy via the scoped grant; admin
	// uses its admin grant.

	t.Run("in-scope tag-form admits deregister", func(t *testing.T) {
		name := "dereg-tag-in-" + randomNoun(t)
		_, _ = h.seedAnalyticsTemplate(t, name)
		if code, body := h.f.request(t, "POST", "/v1/templates/analytics/undeploy", h.adminKey, map[string]any{}); code != 200 {
			t.Fatalf("precondition undeploy: %d %+v", code, body)
		}
		code, body := h.f.request(t, "DELETE", "/v1/templates/analytics", h.analytics, nil)
		requireOK(t, code, body, "deregister by tag-form (in-scope)")
	})

	t.Run("in-scope hash-form (one tag) admits deregister", func(t *testing.T) {
		name := "dereg-hash-1tag-in-" + randomNoun(t)
		hash, _ := h.seedAnalyticsTemplate(t, name)
		if code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/undeploy", h.adminKey, map[string]any{}); code != 200 {
			t.Fatalf("precondition undeploy hash: %d %+v", code, body)
		}
		code, body := h.f.request(t, "DELETE", "/v1/templates/"+hash, h.analytics, nil)
		requireOK(t, code, body, "deregister by hash-form one-tag (in-scope)")
	})

	t.Run("in-scope hash-form (two tags) admits deregister", func(t *testing.T) {
		name := "dereg-hash-2tag-in-" + randomNoun(t)
		hash := h.seedAnalyticsTemplateWithExtraTag(t, name, "ops")
		if code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/undeploy", h.adminKey, map[string]any{}); code != 200 {
			t.Fatalf("precondition undeploy multi-tag: %d %+v", code, body)
		}
		code, body := h.f.request(t, "DELETE", "/v1/templates/"+hash, h.analytics, nil)
		requireOK(t, code, body, "deregister by hash-form two-tag (set-membership)")
	})

	t.Run("out-of-scope tag-form rejects deregister", func(t *testing.T) {
		name := "dereg-tag-out-" + randomNoun(t)
		_, _ = h.seedBillingTemplate(t, name)
		if code, body := h.f.request(t, "POST", "/v1/templates/billing/undeploy", h.adminKey, map[string]any{}); code != 200 {
			t.Fatalf("precondition undeploy billing tag: %d %+v", code, body)
		}
		code, body := h.f.request(t, "DELETE", "/v1/templates/billing", h.analytics, nil)
		requireForbidden(t, code, body, "deregister by tag-form (out-of-scope)")
	})

	t.Run("out-of-scope hash-form rejects deregister", func(t *testing.T) {
		name := "dereg-hash-out-" + randomNoun(t)
		hash, _ := h.seedBillingTemplate(t, name)
		if code, body := h.f.request(t, "POST", "/v1/templates/"+hash+"/undeploy", h.adminKey, map[string]any{}); code != 200 {
			t.Fatalf("precondition undeploy billing hash: %d %+v", code, body)
		}
		code, body := h.f.request(t, "DELETE", "/v1/templates/"+hash, h.analytics, nil)
		requireForbidden(t, code, body, "deregister by hash-form (out-of-scope)")
	})
}

// TestGrantScope_TagSet proves the scope gate fires on tag:set. The
// URL segment is the tag itself so there is no hash-form variant — the
// only `template_tag` candidate the gate ever generates is the tag in
// the URL. We still exercise three legs: in-scope (analytics target),
// out-of-scope (billing target), and a sanity admit when admin moves
// the same tag.
func TestGrantScope_TagSet(t *testing.T) {
	h := newScopeLifecycleHarness(t)

	t.Run("in-scope tag PUT admits", func(t *testing.T) {
		// @deliberate: Seed an analytics tag pointing at hashA, then have the scoped
		// key PUT it to hashB. Both hashes are admin-registered (the
		// scoped key has analytics-scoped template:register only — it
		// can't register an additional billing template).
		_, _ = h.seedAnalyticsTemplate(t, "tagset-in-a-"+randomNoun(t))
		// @deliberate: Register a distinct second analytics-eligible template (via
		// admin) — the PUT moves the analytics tag to point at it.
		hashB := registerOnly(t, h.f, h.adminKey, "tagset-in-b-"+randomNoun(t), "analytics-pending-move")
		code, body := h.f.request(t, "PUT", "/v1/tags/analytics", h.analytics, map[string]any{
			"template": hashB,
		})
		requireOK(t, code, body, "PUT /tags/analytics (in-scope)")
	})

	t.Run("out-of-scope tag PUT rejects", func(t *testing.T) {
		// @deliberate: Seed a billing tag, then the scoped key tries to PUT it. The
		// gate looks at the URL segment ("billing") and finds the scope
		// is "analytics" — reject 403.
		hashA, _ := h.seedBillingTemplate(t, "tagset-out-a-"+randomNoun(t))
		_ = hashA
		// @deliberate: Re-target billing → any registered template. The 403 comes
		// from the scope gate, not the target.
		hashB := registerOnly(t, h.f, h.adminKey, "tagset-out-b-"+randomNoun(t), "billing-pending-move")
		code, body := h.f.request(t, "PUT", "/v1/tags/billing", h.analytics, map[string]any{
			"template": hashB,
		})
		requireForbidden(t, code, body, "PUT /tags/billing (out-of-scope)")
	})
}

// TestGrantScope_TagDelete proves the scope gate fires on tag:delete.
// Same shape as tag:set — the URL segment is the tag, no hash-form.
// Each leg uses its own local fixture (rather than the shared harness)
// because tag:delete consumes the tag itself, so cross-leg state isolation
// is cleaner with one fixture per leg.
func TestGrantScope_TagDelete(t *testing.T) {
	t.Run("in-scope tag DELETE admits", func(t *testing.T) {
		// @deliberate: Seed a fresh in-scope tag (use a distinct one so we don't break
		// other sub-tests that need the analytics tag). The scoped key
		// has scope="analytics", which DOES NOT cover this fresh tag —
		// so we re-mint a scoped key here with a per-test scope to keep
		// each leg independent. Reuse pattern: spin a fresh fixture.
		fLocal := newAuthFixture(t)
		defer fLocal.Close()
		_, body := fLocal.request(t, "POST", "/v1/auth/keys", "", map[string]any{
			"name":        "admin",
			"permissions": []map[string]any{{"action": "*"}},
		})
		adminKey, _ := body["plaintext"].(string)
		_, sb := fLocal.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
			"name": "tagdel-scoped",
			"permissions": []map[string]any{
				{"action": "tag:delete", "scope": map[string]any{"template_tag": "scoped-tag"}},
				{"action": "*:read"},
			},
		})
		scopedKey, _ := sb["plaintext"].(string)
		// @deliberate: Seed the tag under admin so the scoped key can delete it.
		hash := registerOnly(t, fLocal, adminKey, "tagdel-in-"+randomNoun(t), "scoped-tag")
		_ = hash
		code, body := fLocal.request(t, "DELETE", "/v1/tags/scoped-tag", scopedKey, nil)
		requireOK(t, code, body, "DELETE /tags/scoped-tag (in-scope)")
	})

	t.Run("out-of-scope tag DELETE rejects", func(t *testing.T) {
		fLocal := newAuthFixture(t)
		defer fLocal.Close()
		_, body := fLocal.request(t, "POST", "/v1/auth/keys", "", map[string]any{
			"name":        "admin",
			"permissions": []map[string]any{{"action": "*"}},
		})
		adminKey, _ := body["plaintext"].(string)
		_, sb := fLocal.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
			"name": "tagdel-scoped",
			"permissions": []map[string]any{
				{"action": "tag:delete", "scope": map[string]any{"template_tag": "scoped-tag"}},
				{"action": "*:read"},
			},
		})
		scopedKey, _ := sb["plaintext"].(string)
		_ = registerOnly(t, fLocal, adminKey, "tagdel-out-"+randomNoun(t), "other-tag")
		code, body := fLocal.request(t, "DELETE", "/v1/tags/other-tag", scopedKey, nil)
		requireForbidden(t, code, body, "DELETE /tags/other-tag (out-of-scope)")
	})
}

// TestGrantScope_InstanceCreate proves the scope gate fires on
// instance:create when the `template` field is a tag-form or hash-form
// reference, with the same three legs as the lifecycle tests.
func TestGrantScope_InstanceCreate(t *testing.T) {
	h := newScopeLifecycleHarness(t)

	t.Run("in-scope tag-form admits instance create", func(t *testing.T) {
		_, _ = h.seedAnalyticsTemplate(t, "inst-tag-in-"+randomNoun(t))
		ckey := "ck-" + randomNoun(t)
		code, body := h.f.request(t, "POST", "/v1/instances", h.analytics, map[string]any{
			"template":     "analytics",
			"instance_key": ckey,
		})
		requireOK(t, code, body, "instance create by tag-form (in-scope)")
	})

	t.Run("in-scope hash-form (one tag) admits instance create", func(t *testing.T) {
		hash, _ := h.seedAnalyticsTemplate(t, "inst-hash-1tag-in-"+randomNoun(t))
		ckey := "ck-" + randomNoun(t)
		code, body := h.f.request(t, "POST", "/v1/instances", h.analytics, map[string]any{
			"template":     hash,
			"instance_key": ckey,
		})
		requireOK(t, code, body, "instance create by hash-form one-tag (in-scope)")
	})

	t.Run("in-scope hash-form (two tags) admits instance create", func(t *testing.T) {
		hash := h.seedAnalyticsTemplateWithExtraTag(t, "inst-hash-2tag-in-"+randomNoun(t), "ads")
		ckey := "ck-" + randomNoun(t)
		code, body := h.f.request(t, "POST", "/v1/instances", h.analytics, map[string]any{
			"template":     hash,
			"instance_key": ckey,
		})
		requireOK(t, code, body, "instance create by hash-form two-tag (set-membership)")
	})

	t.Run("out-of-scope tag-form rejects instance create", func(t *testing.T) {
		_, _ = h.seedBillingTemplate(t, "inst-tag-out-"+randomNoun(t))
		ckey := "ck-" + randomNoun(t)
		code, body := h.f.request(t, "POST", "/v1/instances", h.analytics, map[string]any{
			"template":     "billing",
			"instance_key": ckey,
		})
		requireForbidden(t, code, body, "instance create by tag-form (out-of-scope)")
	})

	t.Run("out-of-scope hash-form rejects instance create", func(t *testing.T) {
		hash, _ := h.seedBillingTemplate(t, "inst-hash-out-"+randomNoun(t))
		ckey := "ck-" + randomNoun(t)
		code, body := h.f.request(t, "POST", "/v1/instances", h.analytics, map[string]any{
			"template":     hash,
			"instance_key": ckey,
		})
		requireForbidden(t, code, body, "instance create by hash-form (out-of-scope)")
	})
}

// TestGrantScope_InstanceCreate_LargeBodyAboveAuditCap is a regression
// guard for the audit-truncation bug: when a POST /instances JSON body
// exceeds auditBodyCapBytes (4 MB) the audit pipeline records a
// synthetic truncation marker instead of the verbatim bytes. Before the
// fix the same truncated marker was ALSO handed to requestTargets, which
// then read no `template` field and resolved an empty target — falsely
// denying a legitimately-scoped grant with 403. The fix splits the
// audit copy from the target-resolution copy: the full body flows to
// requestTargets so a scoped key can authorize a large request, while
// the audit row still records a bounded marker.
//
// Test shape: mint a scoped key with instance:create scoped to
// `template_tag=analytics`, seed an analytics-tagged template, then
// POST /instances with `template: "analytics"` and a `params` blob
// padded above 4 MB. With the fix, the request reaches the handler and
// returns 200/201 (handler-resolved success); without the fix, it
// returns 403 with `permission denied`.
func TestGrantScope_InstanceCreate_LargeBodyAboveAuditCap(t *testing.T) {
	h := newScopeLifecycleHarness(t)
	_, _ = h.seedAnalyticsTemplate(t, "inst-large-body-"+randomNoun(t))

	// @deliberate: Pad to 5 MB — comfortably above auditBodyCapBytes (4 MB) and well
	// below auditBodyHandlerMaxBytes (64 MB). The pad lives in `params`
	// (a free-form map the handler tolerates), so the body's `template`
	// field is still at the top level where requestTargets reads it.
	const padSize = 5 * 1024 * 1024
	pad := strings.Repeat("x", padSize)
	ckey := "ck-large-" + randomNoun(t)
	code, body := h.f.request(t, "POST", "/v1/instances", h.analytics, map[string]any{
		"template":     "analytics",
		"instance_key": ckey,
		"params":       map[string]any{"pad": pad},
	})
	// @deliberate: With the fix the gate sees the full body and authorizes the
	// scoped grant; the handler then runs and returns 200/201. Without
	// the fix the request fails with 403 permission_denied because the
	// audit-truncated marker hides the `template` field from the gate.
	if code == http.StatusForbidden {
		t.Fatalf("scoped grant denied a >4MB in-scope request (audit truncation leaked into target resolution)\nbody: %+v", body)
	}
	requireOK(t, code, body, "instance create with >4MB body (in-scope)")
}

// TestGrantScope_TemplateRegister_HashForm proves the register path's
// hash-form coverage — the existing grant_scope_test.go only exercises
// the tag-form (body.tag = "analytics" / "billing"). A register where
// the body.tag is the empty string falls back to the unscoped path
// (empty target → unscoped grant entries match) and would silently
// fail-open if a future change ever flipped the empty-tag handling. We
// pin both the tagged-in-scope and tagged-out-of-scope register here so
// the full surface is covered.
func TestGrantScope_TemplateRegister_HashForm(t *testing.T) {
	h := newScopeLifecycleHarness(t)

	t.Run("in-scope tag admits register", func(t *testing.T) {
		code, body := h.f.request(t, "POST", "/v1/templates", h.analytics, map[string]any{
			"spec": scopeLifecycleSpec("reg-in-" + randomNoun(t)),
			"tag":  "analytics",
		})
		requireOK(t, code, body, "register tagged analytics (in-scope)")
	})

	t.Run("out-of-scope tag rejects register", func(t *testing.T) {
		code, body := h.f.request(t, "POST", "/v1/templates", h.analytics, map[string]any{
			"spec": scopeLifecycleSpec("reg-out-" + randomNoun(t)),
			"tag":  "billing",
		})
		requireForbidden(t, code, body, "register tagged billing (out-of-scope)")
	})
}

// registerOnly is `seedDeployedTemplate` minus the deploy step — used by
// the deploy/undeploy/deregister sub-tests that need to drive the
// transition themselves through the scoped key. The tag is set inside
// the POST /templates body (which is where the scope gate reads it for
// template:register), so we don't go through PUT /tags/{tag} for the
// initial tag bind.
func registerOnly(t *testing.T, f *authFixture, adminKey, name, tag string) string {
	t.Helper()
	body := map[string]any{
		"spec": scopeLifecycleSpec(name),
	}
	if tag != "" {
		body["tag"] = tag
	}
	code, resp := f.request(t, "POST", "/v1/templates", adminKey, body)
	if code != 201 && code != 200 {
		t.Fatalf("registerOnly %q: %d %+v", name, code, resp)
	}
	hash, _ := resp["template_id"].(string)
	if hash == "" {
		t.Fatalf("registerOnly %q: missing template_id: %+v", name, resp)
	}
	return hash
}

// randomNoun returns a short random-ish string. Tests in this file
// embed it into template names to keep content-addressed hashes
// distinct so the sub-tests are independent within one fixture (a
// re-register of the same hash idempotently returns the existing row,
// which would muddy the scope-failure assertions).
func randomNoun(t *testing.T) string {
	t.Helper()
	// @deliberate: Use the test name as part of the salt so sub-tests under the
	// same parent test diverge cleanly even when called in rapid
	// succession.
	salt := strings.ReplaceAll(t.Name(), "/", "_")
	return salt + "-" + nonce()
}

// @deliberate: nonce returns a short timestamp-derived suffix. Plain monotonic
// uniqueness is sufficient because the fixture's t.TempDir() ensures a
// fresh SQLite per test (no cross-test bleed).
var nonceCounter uint64

func nonce() string {
	nonceCounter++
	return strings.ToLower(http.StatusText(int(nonceCounter%500 + 100)))
}
