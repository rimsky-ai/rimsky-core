---
audit: api-key-management
artifact: story:api-key-management
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:16Z
---

# Operator can bootstrap, mint, list, show, revoke, rotate, and check status of api-keys

Supported. `rimsky auth init` (`cmd/rimsky/cli/auth_init.go`) mints the first admin key against an anonymous-mode deployment and refuses a second run once the deployment is authenticated; the control API implements mint (`POST /v1/auth/keys`), list (`GET /v1/auth/keys`), show (`GET /v1/auth/keys/{nameOrID}`), revoke (`DELETE`, with a `force_leave_anonymous` guard), rotate (grace-window rollover that keeps the old key valid until a scheduled revoke, with a scheduler sweep job retiring it after the grace), and status (`GET /v1/auth/status`) in `lib/control/controlapi/auth_handlers.go`. The list/show DTO (`keyDTO`) carries no plaintext field — only mint and rotate responses surface plaintext, exactly once. Coverage was checked via the CLI command files under `cmd/rimsky/cli/auth_*.go` (init, create, list, show, rotate, revoke, status, login — 8 files, one per lifecycle verb plus login) and the corresponding scenario/unit suites (`test/scenarios/auth/anonymous_mode_bootstrap_e2e_test.go`, `lib/control/controlapi/auth_rotate_handlers_test.go`, `auth_rotate_revoked_test.go`, `auth_expiry_guard_test.go`, `lib/control/config/scheduler_auth_sweep_test.go`), which together exercise bootstrap, mint, list, show, revoke, rotate-with-grace, and status end to end.
