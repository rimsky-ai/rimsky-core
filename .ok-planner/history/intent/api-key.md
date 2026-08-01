# Intent Dossier: api-key

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- API keys are bearer tokens: high-entropy `rk_<44-char-base64url>` (33 bytes CSPRNG), presented via Authorization: Bearer, SHA-256-hashed at rest in rimsky_api_keys, each with a required immutable unique name and a JSONB permission grant. Simple, stateless, service-account-friendly.
- Plaintext is surfaced exactly once — at mint and at each rotation; the server retains only the hash; lost plaintext means rotate.
- Keys are revoked, never deleted: revoked_at is set and the row persists so audit records stay joinable; the orphan reaper must not touch rimsky_api_keys.
- Anonymous mode is data-derived (zero active keys ⇒ unauthenticated access), never config-derived; the server's anonymous-mode predicate is the authoritative bootstrap gate.
- Rotation is atomic and identity-preserving with a grace window (default 24h) during which both keys are valid; a scheduler sweep revokes past-grace keys.
- A grant entry may carry `mode: dry_run` (identity-bound dry-run floor) and per-action resource scope; both are enforced, not just parsed (see permission dossier).
- On the wire, the authentication path is the sender discriminator: operator API key vs publisher-subscription capability; sender_kind was dropped from the message envelope and is stamped server-side from auth context.
- The api-key ledger is the ENTIRE principal registry — no user entity exists, so a service principal IS an api-key. Under `peer_auth: mtls` an operator-deployed service holds a key carrying the new `service:enroll` grant and exchanges it at POST /v1/enroll for a short-lived certificate identity; the api-key is the standing secret, the cert is the derived identity, and revoking the key stops its renewal (2026-07-16, peer-auth-posture, transcript; see the peer-auth dossier).

## Required behaviors (open promises)

- Token format and storage: rk_ prefix, 44-char base64url, SHA-256 at rest, immutable unique name, JSONB grant (2026-05-15, control-plane-mcp-and-auth, artifact): "surfaced once at mint (and once per rotation), hashed (SHA-256) for storage."
- Plaintext exactly once (2026-05-15, artifact): "The server retains only key_hash." Metadata reads never expose plaintext (2026-06-08, corpus-bootstrap, artifact).
- Revoked-never-deleted invariant; reaper exclusion (2026-05-15, artifact): "API keys are revoked, not deleted … The orphan reaper does not touch rimsky_api_keys."
- Rotation: mints a new key with the same name/permissions/expires_at, sets revoke_at = now + grace on the old, returns new plaintext; both valid during grace; a partial unique index lets the two rows share the name; a ~1-minute scheduler sweep revokes past-grace keys with reason rotation_grace; rotation cannot change expires_at (2026-05-15, artifact). Old key keeps working through the grace window then stops (2026-06-08, corpus-bootstrap, artifact).
- Rotating a revoked key is refused with 409 (defensive gate: it would mint an active key with revoked-key permissions) (2026-05-15, plan-divergences, artifact) `(artifact-only)`.
- Revoke-last-key guard: refuse with 409 if revocation would return the deployment to anonymous mode, unless force_leave_anonymous=true (2026-05-15, artifact).
- Bootstrap: `rimsky auth init` calls POST /auth/keys unauthenticated while anonymous, mints the admin key from the bundled admin role, prints plaintext once, refuses if the keys table is non-empty (CLI check is a UX nicety; the server predicate is authoritative); anonymous mode closes the moment the first key is minted and subsequent unauthenticated requests are refused; an auth-status surface reports mode and active key count (2026-05-15 + 2026-06-08, artifact).
- 401 bodies carry a typed denial_reason (no_token / revoked_token / expired_token); GET /auth/keys wraps results in a {"keys":[...]} envelope (2026-05-15, plan-divergences, artifact) `(artifact-only)`.
- auth:create dry-run mints no plaintext and returns a placeholder id; in anonymous mode the response notes that committing the first key exits anonymous mode (2026-05-29, console-upstream-auth-audit, artifact) `(artifact-only)`.
- Identity-bound dry-run floor and grant scope enforcement ride the key (2026-06-06 + 2026-06-08, artifact — see permission dossier for full detail).
- Role JSON expands into the grant at key-creation time and the minted key enforces exactly that grant through the real gate (2026-06-02, acceptance-coverage-recovery, artifact).
- Audit-log read: GET /audit (audit:read) returns every auth-relevant action — creates, revokes, rotates, dry-run attempts, denied attempts — timestamp-ordered with actor identity, action, outcome, resource target (2026-06-08, corpus-bootstrap, artifact).
- rimsky_instances.created_by_api_key_id (nullable UUID FK) records the creating key, sourced from IdentityFromContextOK's KeyID — never from requestingKeyID, which returns the literal "anonymous" (2026-05-24, host-agent-and-proxy, artifact).
- Host-agent auth: the agent dials the proxy outbound with the user's api-key (no inbound route); latest Register under the same key wins, older connection gracefully closed with displaced_prior=true on the new RegisterAck; `rimsky auth login` writes the api-key into the active CLI context (env RIMSKY_API_KEY + RIMSKY_URL override) (2026-05-24, artifact).
- Persistence house pattern: every APIKeyTable method takes a nullable Tx; ActiveCount takes an injected now; audit writes bridge typed payloads inside Tables.Transaction; the rotation-grace sweep is wired in the shared scheduler-config constructor alongside the other sweeps (2026-05-15, plan-divergences, artifact) `(artifact-only)`.
- Sender discrimination by auth path: sender_kind is not on the wire envelope; the server stamps the persistence row's sender_kind (operator | publisher | instance) from auth context, with 'instance' stamped only by the runtime cascade-emit path (2026-06-19, 8a3b8c19, transcript).

## Intentional absences

- External identity providers (OIDC, SAML, JWT validation, mTLS termination at the edge): out of scope by design, not deferred — the deployment layer owns EXTERNAL identity; rimsky's surface is the API-key floor (2026-05-15, artifact). Note: this is distinct from the INTERNAL service↔service mTLS added 2026-07-16, which derives from the api-key ledger via `service:enroll` and does not terminate any external identity (2026-07-16, peer-auth-posture, transcript).
- Slow hashing (argon2id/bcrypt): rejected — full-entropy CSPRNG tokens have no dictionary to guess; slow hashing costs every request for no benefit (2026-05-15, artifact).
- A CLI break-glass verb for a lost admin key: deliberately absent; break-glass is a documented direct-DB operation (2026-05-15, artifact).
- sender_kind on the wire message envelope: dropped; the auth path discriminates (2026-06-19, transcript).
- `--key` meaning instance key: retired; --key means API key globally, instance key is --instance-key (2026-05-15, artifact).

## Corrections and restorations (drift-fight record)

- Dropped test protections acknowledged at execution: no per-subcommand CLI auth unit tests, no anonymous-predicate cache-invalidation test, no TestAuthDryRunIgnored — the auth handlers' ignore-dry-run behavior is enforced only by the absence of a code path (2026-05-15, plan-divergences, artifact). Known gap, never remediated in the record.
- The smoke-test suite (including auth smoke tests) was relocated wholesale out of this repo with the production stores; auth/observability smoke tests were flagged as candidates for recreation against a stub-only harness (2026-05-24, repo-reorganization-test-audit, artifact).
- Scoped keys silently over-granting was ruled a defect and scope enforcement un-deferred (2026-06-06, artifact — precedent: parse-but-not-enforce on a security surface is fix-code).

## Superseded / historical

- Grant-mode-free keys with request-flag-only dry-run (2026-05-29) → identity-bindable dry_run floor on the grant (2026-06-06). See permission dossier.
- V2 deferral of resource scoping (2026-05-15) → un-deferred (2026-06-06).
- Three-valued sender_kind enum on the wire with 'instance' rejected at the HTTP boundary → auth-path discrimination, server-side stamping (2026-06-19, transcript).
