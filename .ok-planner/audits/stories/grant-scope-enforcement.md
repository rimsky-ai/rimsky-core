---
audit: grant-scope-enforcement
artifact: story:grant-scope-enforcement
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:16Z
---

# A scoped api-key grant is confined to its resource across the resource's lifecycle

Supported. `lib/foundation/auth/scope.go::ScopeMatches` plus `lib/foundation/auth/check.go::CheckGrant` implement set-membership scope matching (an entry with a scope only allows requests whose target satisfies every scope key), wired into every gated control-API call via `lib/control/controlapi/auth_middleware.go::gateByAction`. The claim is exercised end to end, not just at one call site: `test/scenarios/auth/grant_scope_lifecycle_test.go` scopes a key to a `template_tag` and checks in-scope admission plus out-of-scope 403 across the full resource lifecycle — register, deploy, undeploy, deregister, tag:set, tag:delete, and instance:create — each in both tag-form and hash-form addressing and both one-tag and two-tag (set-membership) cases, an enumerated 7 actions x up to 5 sub-cases each; a further case (`TestGrantScope_RegisterWithTagCannotMoveWithoutTagSet`) confirms a register-scoped key cannot silently move an existing tag without an independent `tag:set` grant, and `TestGrantScope_TemplateTagEnforced` (`grant_scope_test.go`) additionally checks the denial is durably audited (`auth.access_denied` event with `denial_reason=permission_denied`) and that the out-of-scope write never persisted.
