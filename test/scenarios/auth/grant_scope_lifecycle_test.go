// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: permission

package auth_test

import (
	"net/http"
	"strings"
	"testing"
)

func scopeLifecycleSpec(name string) map[string]any {
	return map[string]any{
		"name":    name,
		"version": "1",
		"nodes":   []map[string]any{{"type": "n1"}},
	}
}

type scopeLifecycleHarness struct {
	f         *authFixture
	adminKey  string
	analytics string
}

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

	return &scopeLifecycleHarness{f: f, adminKey: adminKey, analytics: scopedKey}
}

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
	code, depResp := h.f.request(t, "POST", "/v1/templates/"+hash+"/deploy", h.adminKey, map[string]any{})
	if code != 200 {
		t.Fatalf("seed analytics deploy %q: %d %+v", name, code, depResp)
	}
	return hash, "analytics"
}

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

func requireForbidden(t *testing.T, code int, body map[string]any, what string) {
	t.Helper()
	if code != 403 {
		t.Fatalf("%s: got %d, want 403 (scope-denied)\nbody: %+v", what, code, body)
	}
}

func requireOK(t *testing.T, code int, body map[string]any, what string) {
	t.Helper()
	if code != 200 && code != 201 {
		t.Fatalf("%s: got %d, want 200/201 (scope matches)\nbody: %+v", what, code, body)
	}
}

func TestGrantScope_TemplateDeploy(t *testing.T) {
	h := newScopeLifecycleHarness(t)

	t.Run("in-scope tag-form admits deploy", func(t *testing.T) {
		name := "deploy-tag-in-" + randomNoun(t)
		hash := registerOnly(t, h.f, h.adminKey, name, "analytics")
		code, body := h.f.request(t, "POST", "/v1/templates/analytics/deploy", h.analytics, map[string]any{})
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

func TestGrantScope_TemplateDeregister(t *testing.T) {
	h := newScopeLifecycleHarness(t)

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

func TestGrantScope_TagSet(t *testing.T) {
	h := newScopeLifecycleHarness(t)

	t.Run("in-scope tag PUT admits", func(t *testing.T) {
		_, _ = h.seedAnalyticsTemplate(t, "tagset-in-a-"+randomNoun(t))
		hashB := registerOnly(t, h.f, h.adminKey, "tagset-in-b-"+randomNoun(t), "analytics-pending-move")
		code, body := h.f.request(t, "PUT", "/v1/tags/analytics", h.analytics, map[string]any{
			"template": hashB,
		})
		requireOK(t, code, body, "PUT /tags/analytics (in-scope)")
	})

	t.Run("out-of-scope tag PUT rejects", func(t *testing.T) {
		hashA, _ := h.seedBillingTemplate(t, "tagset-out-a-"+randomNoun(t))
		_ = hashA
		hashB := registerOnly(t, h.f, h.adminKey, "tagset-out-b-"+randomNoun(t), "billing-pending-move")
		code, body := h.f.request(t, "PUT", "/v1/tags/billing", h.analytics, map[string]any{
			"template": hashB,
		})
		requireForbidden(t, code, body, "PUT /tags/billing (out-of-scope)")
	})
}

func TestGrantScope_TagDelete(t *testing.T) {
	t.Run("in-scope tag DELETE admits", func(t *testing.T) {
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

func TestGrantScope_RegisterWithTagCannotMoveWithoutTagSet(t *testing.T) {
	fLocal := newAuthFixture(t)
	defer fLocal.Close()

	_, body := fLocal.request(t, "POST", "/v1/auth/keys", "", map[string]any{
		"name":        "admin",
		"permissions": []map[string]any{{"action": "*"}},
	})
	adminKey, _ := body["plaintext"].(string)

	tag := "moveguard-" + randomNoun(t)
	firstHash := registerOnly(t, fLocal, adminKey, "moveguard-first-"+randomNoun(t), tag)

	_, sb := fLocal.request(t, "POST", "/v1/auth/keys", adminKey, map[string]any{
		"name": "register-only-no-tagset",
		"permissions": []map[string]any{
			{"action": "template:register", "scope": map[string]any{"template_tag": tag}},
			{"action": "*:read"},
		},
	})
	scopedKey, _ := sb["plaintext"].(string)

	code, resp := fLocal.request(t, "POST", "/v1/templates", scopedKey, map[string]any{
		"spec": scopeLifecycleSpec("moveguard-second-" + randomNoun(t)),
		"tag":  tag,
	})
	requireForbidden(t, code, resp,
		"register-with-tag must not move an existing tag for a caller holding template:register on that tag but not tag:set")

	code, getResp := fLocal.request(t, "GET", "/v1/templates/"+tag, adminKey, nil)
	requireOK(t, code, getResp, "re-fetch tag after rejected move")
	if getResp["id"] != firstHash {
		t.Fatalf("tag %q moved despite the forbidden request: got %v want %v", tag, getResp["id"], firstHash)
	}
}

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

func TestGrantScope_InstanceCreate_LargeBodyAboveAuditCap(t *testing.T) {
	h := newScopeLifecycleHarness(t)
	_, _ = h.seedAnalyticsTemplate(t, "inst-large-body-"+randomNoun(t))

	const padSize = 5 * 1024 * 1024
	pad := strings.Repeat("x", padSize)
	ckey := "ck-large-" + randomNoun(t)
	code, body := h.f.request(t, "POST", "/v1/instances", h.analytics, map[string]any{
		"template":     "analytics",
		"instance_key": ckey,
		"params":       map[string]any{"pad": pad},
	})
	if code == http.StatusForbidden {
		t.Fatalf("scoped grant denied a >4MB in-scope request (audit truncation leaked into target resolution)\nbody: %+v", body)
	}
	requireOK(t, code, body, "instance create with >4MB body (in-scope)")
}

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

func randomNoun(t *testing.T) string {
	t.Helper()
	salt := strings.ReplaceAll(t.Name(), "/", "_")
	return salt + "-" + nonce()
}

var nonceCounter uint64

func nonce() string {
	nonceCounter++
	return strings.ToLower(http.StatusText(int(nonceCounter%500 + 100)))
}
