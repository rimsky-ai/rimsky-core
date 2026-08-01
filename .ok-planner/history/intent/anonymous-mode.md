# Intent Dossier: anonymous-mode

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Anonymous mode is a **derived deployment state**: it holds exactly when `rimsky_api_keys` has zero active rows. It is data-derived, never config-derived — no config bit, no auth-mode knob, no bootstrap key in `rimsky.yml`. The data state of `rimsky_api_keys` is the sole source of truth (2026-05-15, corroborated 2026-06-08).
- While anonymous, every **credential-less** request gets a synthetic admin identity: `key_id` null, `key_name` "anonymous", effective permissions `[{action:'*'}]`; audit records reflect this. (User ruling 2026-07-17: admission is scoped to requests presenting no Authorization header; a presented-but-bad token — malformed, unknown, expired, revoked — is rejected 401 even while anonymous, failing closed rather than silently promoting a stale-credentialed client. The earlier "every request" wording never contemplated the presented-token case.)
- The mode ends automatically the moment the first key is minted; toggling between anonymous and authenticated is automatic on the first/last active key. After the first admin key mints via `rimsky auth init`, subsequent unauthenticated requests are refused, and the status surface accurately reports the mode throughout (2026-06-08).
- Bootstrap exploits anonymous mode with no separate mechanism; the server's anonymous-mode predicate is the authoritative gate, the CLI's refuse-if-keys-exist check only a UX nicety. Break-glass for a lost admin key is a documented direct-DB operation, deliberately not a CLI verb (2026-05-15).
- Anonymous-mode instances (null `created_by_api_key_id`) are **not** locked out of host-agent late-binding — the earlier hard mutual exclusion was resolved on 2026-06-06.

## Required behaviors (open promises)

All entries on this concept are artifact-tier; single-source items are flagged per rule 3.

- Zero active `rimsky_api_keys` rows → synthetic admin identity on every request; control-api logs a loud WARN banner at startup and every 5 minutes while anonymous (2026-05-15, control-plane-mcp-and-auth, artifact): "Every request gets a synthetic admin identity; audit records reflect this; loud startup warnings." Core semantics corroborated by 2026-06-08 corpus-bootstrap; the 5-minute banner cadence is (artifact-only).
- No auth-related keys in `rimsky.yml` at all — mode computed solely from `rimsky_api_keys` row counts (2026-05-15, control-plane-mcp-and-auth, artifact): "rimsky.yml carries no auth-mode knob."
- Revoking the last active key is guarded: DELETE /auth/keys refuses with 409 if it would return the deployment to anonymous mode, unless `force_leave_anonymous=true` is passed explicitly — never default (2026-05-15, control-plane-mcp-and-auth, artifact) (artifact-only).
- The anonymous-mode predicate may be cached per control-api replica with a short TTL (default 1s); TTL is the only freshness mechanism, no cross-process invalidation channel (2026-05-15, control-plane-mcp-and-auth, artifact) (artifact-only).
- `rimsky auth init` calls POST /auth/keys unauthenticated while anonymous, mints the admin key from the bundled admin role, prints the plaintext once; the server predicate, not the CLI check, is the gate (2026-05-15, control-plane-mcp-and-auth, artifact) (artifact-only).
- On a fresh deployment all control-api and CLI actions succeed unauthenticated; minting the first admin key closes the mode: "the moment I mint the first admin key, anonymous mode closes and subsequent unauthenticated requests are refused" (2026-06-08, corpus-bootstrap, artifact).
- An auth:create dry-run in anonymous mode notes in its response that committing the first key exits anonymous mode and requires auth on all future requests (2026-05-29, console-upstream-auth-audit-and-fixes, artifact) (artifact-only).
- `rimsky_instances.created_by_api_key_id` is populated from `IdentityFromContextOK`'s KeyID — never from `requestingKeyID`, which returns the literal string "anonymous" in anonymous mode and cannot parse to UUID (2026-05-24, host-agent-and-proxy, artifact) (artifact-only).
- Late-binding works for instances created in anonymous mode: a late-bound dispatch from an ownerless instance resolves the serving agent via an anonymous-mode routing identity / default agent route, and "must not terminate host_agent_not_connected merely because the instance owner is NULL" (2026-06-06, comprehensive-gap-closure, artifact; corroborated 2026-06-08, corpus-bootstrap, artifact).
- The idempotency-dedup layer's `sender_kind` enum is operator/publisher/**anonymous** (distinct from the envelope's operator/publisher/instance enum): anonymous buckets anonymous-mode operator emits separately so the bootstrap admin's later emits don't dedup against pre-key-mint emits (2026-06-08, corpus-bootstrap, artifact): "The two `sender_kind` columns are not the same enum and should not be conflated." (artifact-only)

## Intentional absences

- **No auth-mode / bootstrap-key config surface** — rejected by design; anonymous state is data-derived only (2026-05-15, control-plane-mcp-and-auth).
- **No break-glass CLI verb** for a lost admin key — deliberately a documented direct-DB operation (revoke/delete all rows to resume anonymous mode), because the operator by definition has DB access in that scenario (2026-05-15, control-plane-mcp-and-auth).
- **No cross-process cache-invalidation channel** for the anonymous predicate — the TTL bounds staleness by design (2026-05-15, control-plane-mcp-and-auth).

## Corrections and restorations (drift-fight record)

- Planned test protections were dropped in execution and never restored per the record: no per-subcommand CLI auth unit tests (only common-helper tests plus an HTTP-level smoke bypassing the CLI functions), no dedicated anonymous-predicate cache-invalidation test, no `TestAuthDryRunIgnored` — "a future change that added a dry-run branch to those handlers would not be caught by any existing test" (2026-05-15, control-plane-mcp-and-auth-plan-divergences, artifact). Known gap, not a retraction of the behaviors themselves.
- The smoke suite's shared BootCluster was not extended to bootstrap auth: only the dedicated auth smoke test runs `rimsky auth init`; every other smoke test exercises control-api in anonymous mode (2026-05-15, control-plane-mcp-and-auth-plan-divergences, artifact).

## Superseded / historical

- 2026-05-24 (host-agent-and-proxy): anonymous-mode instances could not use late-bound services in v1 — the proxy treated null `created_by_api_key_id` as "no agent connected", cataloged as the anonymous-mode-locks-out-late-bind tension. Superseded by the 2026-06-06 comprehensive-gap-closure decision resolving that tension: "anonymous-mode and host-agent late-binding are not mutually exclusive."
