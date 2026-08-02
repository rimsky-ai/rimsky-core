---
audit: auth-grant-scope
artifact: decision:auth-grant-scope
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:16Z
---

# Grant entries carry an optional scope map of action-specific dimension keys

Supported. `lib/foundation/auth/grant.go::GrantEntry` carries an `Action`, optional `Mode`, and optional `Scope map[string]string`, forward-compatibly round-tripping unknown fields; `lib/foundation/auth/scope.go::ScopeMatches` (annotated `@decision: auth-grant-scope`) constrains the action to requests whose target resource satisfies every declared dimension key, so a grant is confined by resource property rather than by a fixed instance identifier — matching the choice over both rejected alternatives (flat ungated grants, and per-resource ACLs). The same evidence used for `story:grant-scope-enforcement` (the `test/scenarios/auth/grant_scope_lifecycle_test.go` and `grant_scope_test.go` suites) exercises this scope dimension — `template_tag` — across 7 distinct actions.
