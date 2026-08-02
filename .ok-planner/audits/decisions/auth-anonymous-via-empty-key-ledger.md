---
audit: auth-anonymous-via-empty-key-ledger
artifact: decision:auth-anonymous-via-empty-key-ledger
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:16Z
---

# Anonymous admission is computed from the api-key ledger's row count, no new provisioning verb

Supported. `lib/control/controlapi/auth_middleware.go::(*AuthState).IsAnonymousMode` computes anonymous mode purely from `Tables.APIKeys().ActiveCount`, with no persisted config bit and no separate provisioning path; `resolveIdentity` substitutes the synthetic admin identity only when that predicate holds and only for credential-less requests. There is no key-minting call anywhere in the anonymous-admission path — the ledger stays empty until an operator explicitly mints a key, matching the "the verb provisions no keys" claim. `test/scenarios/auth/anonymous_mode_bootstrap_e2e_test.go` exercises the full arc (open floor, mint, closed floor) against this mechanism with no separate ephemeral-admin-key code path in evidence.
