---
issue: claude-agent-allowlist-env-parsing-untested
kind: audit
category: test-coverage
artifacts:
  - decision:allowlist-defaults-open
status: repaired
opened: 2026-08-02T09:58:05Z
---

# Does `allowlistFromEnv`/`LoadOptsFromEnv` have test coverage for the default-open/set-empty-closed rule?

`decision:allowlist-defaults-open` requires an operator allowlist env var to be
open when unset and closed when set-but-empty; re-verification confirmed
`claude-agent/opts.go::allowlistFromEnv` already implements this correctly,
but no test exercised it directly.

Rule that determined the fix: this is a pure test-coverage gap over
already-correct, already-committed behavior (`decision:allowlist-defaults-open`'s
Choice is unchanged) — outcome 2 (add a missing test), not a design question.

What changed: added `TestAllowlistFromEnv_UnsetIsOpen`,
`TestAllowlistFromEnv_SetEmptyIsClosed`,
`TestAllowlistFromEnv_SetWithNamesAllowsOnlyThose`, and
`TestLoadOptsFromEnv_AllowlistsRideNamespacedEnvVars` to
`lib/services/executors/claude-agent/opts_test.go`, exercising
`allowlistFromEnv` and `LoadOptsFromEnv` directly against the unset /
set-empty / set-with-names cases.

Verified: `go test ./lib/services/executors/claude-agent/...` passes (new
tests plus the full existing package suite, 24.8s).
