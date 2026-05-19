---
concept: api-key
status: as-is
aliases:
  - bearer token
references:
  - .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
---

# API key

## What it is

A high-entropy, rimsky-issued credential carried by control-api clients as `Authorization: Bearer <key>`. Plaintext format: `rk_<44-char-base64url>` (33 bytes of CSPRNG entropy = 264 bits). The server stores only SHA-256(plaintext) in `table:rimsky_api_keys`; the plaintext is surfaced exactly once at mint and again at each rotation. Implemented under `code:foundation/auth/plaintext.go` (mint/hash/validate helpers) + `code:foundation/persistence/api_keys.go` (row type + table interface) + `code:control/controlapi/auth_middleware.go::AuthState` (per-request lookup).

## Purpose

Rimsky needs an authentication floor: every control-api endpoint should be able to tell who is calling, and operators need a primitive they can mint, rotate, and revoke without redeploying. API keys are the floor — deployments that need richer identity (OIDC, SAML, mTLS) terminate that at their edge and inject API keys downstream.

## Boundaries

Owns: the plaintext format + hash; the `rimsky_api_keys` table; the lifecycle verbs (mint / list / show / revoke / rotate / sweep) at `code:control/controlapi/auth_handlers.go`; the rotation-grace sweep at `code:runtime/auth_sweep.go::SweepRotationGrace`. Does NOT own: external IdP integration, rate-limiting, role definitions (those live CLI-side; see `concept:role-template`). Adjacent: `concept:permission` (the grant attached to each key), `concept:anonymous-mode` (the data-derived deployment state when no active keys exist), `concept:event-log` (auth audit emissions).

## Invariants

- **Plaintext is surfaced exactly once.** At mint and at each rotation. The server retains only the SHA-256 hash. Lost plaintext is unrecoverable; recovery is rotation.
- **Keys are revoked, not deleted.** `revoked_at` is set; the row persists. Preserves the audit trail (`auth.access_*` rows carry `key_id` and join through to the row).
- **Active-status predicate.** A key is active iff `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now) AND (revoke_at IS NULL OR revoke_at > now)`. The middleware applies this on every request; `IsAnonymousMode` consults the same predicate.
- **Name uniqueness is partial.** The `rimsky_api_keys_active_name_idx` index excludes rows in the rotation-grace window so a rotation can mint a new row with the same name while the old one is still active.

## Lifecycle

- **Mint** — `POST /auth/keys`. CSPRNG plaintext minted; SHA-256(plaintext) stored; plaintext surfaced in the response and never persisted. Audit: `auth.key_created`.
- **Rotate** — `POST /auth/keys/{name-or-id}/rotate { grace: <duration> }`. Atomic: sets `revoke_at = now + grace` on the existing row and inserts a new row with the same name + permissions in one transaction. Old key authenticates normally until the grace expires; the rotation-grace sweep (1m cadence, runs in `cmd:rimsky-scheduler`) then revokes it. Audit: `auth.key_rotated` + later `auth.key_revoked { reason: "rotation_grace" }`.
- **Revoke** — `DELETE /auth/keys/{name-or-id}`. Sets `revoked_at = now`. Refuses if the operation would leave zero active keys unless `?force_leave_anonymous=true` is set. Audit: `auth.key_revoked { reason: "manual" }`.

## Notes

- [2026-05-15] Concept introduced by spec `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md` ("Authentication model").
