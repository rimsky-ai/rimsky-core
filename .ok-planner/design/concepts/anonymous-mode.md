---
concept: anonymous-mode
status: as-is
aliases:
  - implicit anonymous mode
---

# Anonymous mode

## What it is

A data-derived deployment state in which the API-key ledger has zero active rows. While in this state, every request — including requests with no `Authorization` header — is admitted as a synthetic admin identity (null key id, name `"anonymous"`, a wildcard `*` permission). The mode flips automatically the moment the first key is minted.

The control-api auth middleware computes the predicate (with a short TTL cache) and substitutes the synthetic anonymous identity when the predicate holds.

## Purpose

The bootstrap problem: a fresh rimsky deployment has no keys, so it can't authenticate anyone, so the key-mint endpoint would be unreachable. Anonymous mode is the floor that lets the first key get minted via the same endpoint operators use thereafter, without a separate database-only bootstrap path.

## Boundaries

Owns: the active-key-count predicate over the API-key ledger, the synthetic-identity helper, the startup WARN banner. Does NOT own: any persistent config bit (the mode is computed; there is no config knob). Adjacent: `concept:api-key`, `concept:rimsky-yml`.

## Invariants

- **Data-derived, not config-derived.** The mode is computed from the count of active keys at request time. There is no config knob. Operators cannot disable anonymous mode without provisioning a key; they cannot stay in anonymous mode after a key exists without explicitly revoking it.
- **Loud startup banner.** Control-api logs at WARN once at startup and every 5 minutes thereafter while in anonymous mode, telling operators that no keys are provisioned, all requests are treated as admin, and that running the auth-init command enables authentication. The banner stops once any active key exists.
- **Predicate caching.** Each control-api replica caches the result for one second. The cache is invalidated on every mutation (create / revoke / rotate / sweep) so the same replica's next request sees the fresh value immediately; cross-replica freshness is bounded by the TTL.
- **Revoke-the-last-key guard.** The key-revoke endpoint refuses if the operation would leave zero active keys unless an explicit force-leave-anonymous flag is supplied. Operators returning the deployment to anonymous mode must do so explicitly.
- **Late-bound services are reachable in anonymous mode.** An instance created in anonymous mode (owner-less, no creating api-key) may still register and dispatch to late-bound services. Routing to the connected dev-machine agent goes through a well-known anonymous routing identity under which an anonymous-mode agent registers, so the dispatch resolves to that agent rather than failing for want of an owner api-key. Anonymous mode and late-binding (`concept:host-agent-proxy`) are not mutually exclusive.

## Bootstrap sequence

1. Operator deploys rimsky; migration runs; the API-key ledger is empty.
2. Control-api starts; predicate is true; banner WARN fires.
3. Operator runs the auth-init command. The CLI posts to the key-mint endpoint with the bundled `admin` role expansion; no bearer token.
4. Server admits the request via the synthetic admin identity; mints the key; returns the plaintext exactly once.
5. Operator captures the plaintext (env var or flag) for subsequent commands.
6. Anonymous mode ends — subsequent unauthenticated requests are rejected as unauthorized.

## Break-glass: lost admin key

If all keys are lost: the operator connects to the database directly and either deletes the key rows or marks them all revoked. With no active key remaining, anonymous mode resumes and the auth-init command works again. Documented as operator-recoverable; no CLI verb required (by definition the operator has DB access).
