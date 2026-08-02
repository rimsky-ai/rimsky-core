---
audit: env-var-registry
artifact: decision:env-var-registry
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:29Z
---

# Generated, fitness-tested registry of every `RIMSKY_*` read; endpoint vars name their target service

Supported. `tools/env-registry/scan/scan.go`'s `LiveReads` walks `cmd/`, `lib/`, `examples/` for `"RIMSKY_..."` literals in non-test, non-generated Go files and renders `tools/env-registry/registry.md` (100 lines, one row per variable); `test/plumbline/env_var_registry_test.go`'s `TestEveryLiveEnvVarReadIsRegistered` re-scans the same population and fails on any mismatch in either direction. The endpoint-naming claim holds for the two named services across every non-test read site found repo-wide: the control API's endpoint is always `RIMSKY_CONTROL_API_URL`/`_HOST`/`_PORT` (CLI included — `cmd/rimsky/cli/run.go`, `templates.go`, `auth_common.go`, `compose/*.go`, `conformance.go`), and the host-agent proxy's endpoint is always `RIMSKY_HOST_AGENT_PROXY_URL` (`lib/runtime/hostagent/config.go`); `lib/services/internal/peerauth/peerauth_test.go` additionally proves the retired generic alias `RIMSKY_ENDPOINT` is deliberately not read.
