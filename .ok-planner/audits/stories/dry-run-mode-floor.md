---
audit: dry-run-mode-floor
artifact: story:dry-run-mode-floor
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:36:49Z
---

# Attempt-only key: dry-run grant floor with an execute-grant carve-out

The codebase carries exactly this mechanism. `auth.CheckGrant` (`lib/foundation/auth/check.go`) resolves every matching grant entry for an action+target and, when more than one matches, prefers `execute` over `dry_run` — so a key holding both a dry-run grant and an execute grant on the same action resolves to execute, matching the story's "provided the key holds no other grant that authorizes execute-mode on that same action" carve-out; this is exercised unit-level, order-independent, and with a scoped-vs-unscoped mix, by `TestCheckGrant_ModeFloor` and `TestCheckGrant_ExecuteBeatsDryRun_OrderIndependent`. The control-API gate (`gateByAction` in `lib/control/controlapi/auth_middleware.go`) then floors the effective request mode to dry-run whenever the resolved grant's mode is dry-run, regardless of the request's own `dry_run` flag. An end-to-end scenario test, `TestDryRun_IdentityBoundFloor`, mints a key scoped to `{action: "instance:create", mode: "dry_run"}`, shows a bare create and an explicit `dry_run=false` create both still return the dry-run envelope and persist nothing (0 instances via a GET re-list), checks the audit log records `executed=false` for the floored attempt, and shows a same-action execute-mode key does commit a real instance. Both the pick-execute-over-dry-run resolution and the floor-on-dry-run enforcement are unit- and scenario-tested.
