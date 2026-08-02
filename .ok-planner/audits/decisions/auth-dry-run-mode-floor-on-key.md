---
audit: auth-dry-run-mode-floor-on-key
artifact: decision:auth-dry-run-mode-floor-on-key
determination: supported
commit: b767a27d
audited: 2026-08-02T09:36:49Z
---

# A dry-run grant pins the key, unoverridable by the request flag

Supported. In `lib/control/controlapi/auth_middleware.go::gateByAction`, the effective mode is computed as `mode := requestedMode` (from the `?dry_run=` query flag) and then unconditionally overwritten to `auth.ModeDryRun` whenever the resolved grant's mode (`res.Mode`, from `auth.CheckGrant`) is dry-run — the request flag is never consulted once the grant floor applies, so a dry-run-mode grant cannot be escalated to a real write by its holder, exactly the choice and rejected-alternative stated. `TestDryRun_IdentityBoundFloor` (`test/scenarios/auth/dry_run_identity_bound_test.go`) proves this directly: a key scoped to `{action: "instance:create", mode: "dry_run"}` gets a dry-run envelope both with no flag and with an explicit `?dry_run=false`, in both cases persisting zero instances, while a same-action key without a dry-run grant commits a real instance. This is the same enforcement point audited for `story:dry-run-mode-floor`.
