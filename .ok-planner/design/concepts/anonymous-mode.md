---
concept: anonymous-mode
status: as-is
aliases:
  - implicit anonymous mode
---

# Anonymous mode

## What it is

A data-derived deployment state in which the API-key ledger has zero active rows. While in this state, every request that presents no credentials is admitted as a synthetic admin identity (null key id, name `"anonymous"`, a wildcard `*` permission). A request that does present credentials is validated normally even while anonymous: a malformed, unknown, expired, or revoked credential is rejected as unauthorized in every mode, so a stale-credentialed client hears about its bad credential instead of being silently promoted to admin. The mode flips automatically the moment the first key is minted.

The mode's predicate is evaluated on every credential-less request (with a short-TTL cache), and the synthetic anonymous identity is substituted whenever the predicate holds.

## Purpose

The bootstrap problem: a fresh rimsky deployment has no keys, so it can't authenticate anyone, so the key-mint endpoint would be unreachable. Anonymous mode is the floor that lets the first key get minted via the same endpoint operators use thereafter, without a separate database-only bootstrap path.

## Boundaries

Owns: the active-key-count predicate over the API-key ledger, the synthetic-identity helper, the startup WARN banner. Does NOT own: any persistent config bit (the mode is computed; there is no config knob). Adjacent: `concept:api-key`, `concept:rimsky-yml`, `concept:host-agent-proxy`.

## Invariants

- **Data-derived, not config-derived.** The mode is computed from the count of active keys at request time. There is no config knob. Operators cannot disable anonymous mode without provisioning a key; they cannot stay in anonymous mode after a key exists without explicitly revoking it.
- **Loud startup banner.** A loud recurring operator-facing warning fires while the deployment is in anonymous mode, telling operators that no keys are provisioned, all requests are treated as admin, and that an operator-directed enable-authentication action stops the banner. The banner stops once any active key exists.
- **Predicate caching.** Each control-api replica caches the result for one second. The cache is invalidated on every key mutation (create / revoke / rotate) so the same replica's next request sees the fresh value immediately; cross-replica freshness is bounded by the TTL. The rotation-grace sweep does not need to invalidate the cache: the active-key count already excludes rows past their revoke time, so the sweep never changes the predicate's result.
- **Revoke-the-last-key guard.** Revoking the last active key refuses unless an explicit intent flag accompanies the request. Operators returning the deployment to anonymous mode must do so explicitly.
- **Expiring-sole-key guard.** Minting the deployment's first key, or rotating its only active key, with a bounded expiry refuses unless an explicit intent flag accompanies the request — the natural lapse of that expiry would otherwise return the deployment to anonymous mode with no explicit action at the moment it happens. A permanent (never-expiring) key, or a create/rotate while other active keys already exist, is unaffected.
- **Late-bound services are reachable in anonymous mode.** An instance created in anonymous mode (no owning api-key) is stamped at creation time with the target anonymous agent's routing identity — a per-agent silly-name, per `concept:host-agent-proxy` — and dispatches resolve to that agent via the ordinary uniform routing rule. Multiple anonymous agents may connect concurrently, each with a distinct silly-name; instances created against different agents do not interfere. Anonymous mode and late-binding are not mutually exclusive.

## Bootstrap sequence

1. Operator deploys rimsky; migration runs; the API-key ledger is empty.
2. Control-api starts; predicate is true; banner WARN fires.
3. Operator runs the bootstrap key-mint command against the ordinary key-mint endpoint, sending no bearer token.
4. Server admits the request via the synthetic admin identity; mints the key; returns the plaintext exactly once.
5. Operator captures the plaintext (env var or flag) for subsequent commands.
6. Anonymous mode ends — identity-gated requests presenting no credentials are now rejected as unauthorized. Routes that never required authentication (health and status probes) are unaffected.

## Break-glass: lost admin key

If all keys are lost: the operator connects to the database directly and either deletes the key rows or marks them all revoked. With no active key remaining, anonymous mode resumes and the bootstrap key-mint flow works again. Operators with database access can return the deployment to anonymous mode.
