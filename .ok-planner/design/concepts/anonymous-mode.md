---
concept: anonymous-mode
status: as-is
aliases:
  - implicit anonymous mode
references:
  - .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
---

# Anonymous mode

## What it is

A data-derived deployment state in which `table:rimsky_api_keys` has zero active rows. While in this state, every request — including requests with no `Authorization` header — is admitted as a synthetic admin identity (`key_id: null`, `key_name: "anonymous"`, permissions `[{ "action": "*" }]`). The mode flips automatically the moment the first key is minted.

Implemented at `code:control/controlapi/auth_middleware.go::AuthState.IsAnonymousMode` (the predicate + TTL cache) + `code:foundation/auth/identity.go::AnonymousIdentity` (the synthetic identity).

## Purpose

The bootstrap problem: a fresh rimsky deployment has no keys, so it can't authenticate anyone, so `POST /auth/keys` would be unreachable. Anonymous mode is the floor that lets the first key get minted via the same endpoint operators use thereafter, without a separate `psql`-only bootstrap path.

## Boundaries

Owns: the active-status predicate on `table:rimsky_api_keys`, the synthetic-identity helper, the startup WARN banner. Does NOT own: any persistent config bit (the mode is computed; there is no `rimsky.yml` knob). Adjacent: `concept:api-key`, `concept:rimsky-yml`.

## Invariants

- **Data-derived, not config-derived.** The mode is computed from the row count of active keys at request time. There is no `rimsky.yml` knob. Operators cannot disable anonymous mode without provisioning a key; they cannot stay in anonymous mode after a key exists without explicitly revoking it.
- **Loud startup banner.** Control-api logs at WARN once at startup and every 5 minutes thereafter while in anonymous mode: `"ANONYMOUS MODE: no API keys provisioned; all requests treated as admin. Run 'rimsky auth init' to enable authentication."`. The banner stops once any active key exists. Implemented at `code:control/controlapi/auth_banner.go::WatchAnonymousMode`.
- **Predicate caching.** Each control-api replica caches the result for 1s (anonCacheTTL). The cache is invalidated on every mutation (create / revoke / rotate / sweep) so the same replica's next request sees the fresh value immediately; cross-replica freshness is bounded by the TTL.
- **Revoke-the-last-key guard.** `DELETE /auth/keys/{name-or-id}` refuses if the operation would leave zero active keys unless `?force_leave_anonymous=true` is set. Operators returning the deployment to anonymous mode must do so explicitly.

## Bootstrap sequence

1. Operator deploys rimsky; migration runs; `rimsky_api_keys` is empty.
2. Control-api starts; predicate is true; banner WARN fires.
3. Operator runs `rimsky auth init`. CLI POSTs to `/auth/keys` with the bundled `admin` role expansion; no Bearer token.
4. Server admits the request via the synthetic admin identity; mints the key; returns the plaintext exactly once.
5. Operator captures the plaintext (env var or `--key` flag) for subsequent commands.
6. Anonymous mode ends — subsequent unauthenticated requests get 401.

## Break-glass: lost admin key

If all keys are lost: the operator opens `psql` and either deletes the rows (`DELETE FROM rimsky_api_keys`) or marks them revoked (`UPDATE rimsky_api_keys SET revoked_at = now() WHERE revoked_at IS NULL`). Anonymous mode resumes; `rimsky auth init` works again. Documented as operator-recoverable; no CLI verb required (by definition the operator has DB access).

## Notes

- [2026-05-15] Concept introduced by spec `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md` ("Implicit anonymous mode").
