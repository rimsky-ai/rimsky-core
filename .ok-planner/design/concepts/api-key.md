---
concept: api-key
status: as-is
aliases:
  - bearer token
---

# API key

## What it is

A high-entropy, rimsky-issued credential carried by control-api clients as `Authorization: Bearer <key>`. Plaintext format: an `rk_` prefix followed by 44 base64url characters (33 bytes of CSPRNG entropy = 264 bits). The server stores only a SHA-256 hash of the plaintext in a persisted API-key ledger; the plaintext is surfaced exactly once at mint and again at each rotation. The mint/hash/validate helpers, the persisted row type, and the per-request middleware lookup together implement the credential.

## Purpose

Rimsky needs an authentication floor: every control-api endpoint should be able to tell who is calling, and operators need a primitive they can mint, rotate, and revoke without redeploying. API keys are the floor — deployments that need richer identity (OIDC, SAML, mTLS) terminate that at their edge and inject API keys downstream.

## Boundaries

Owns: the plaintext format + hash; the persisted API-key ledger; the lifecycle verbs (mint / list / show / revoke / rotate / sweep); the rotation-grace sweep. Does NOT own: external IdP integration, rate-limiting, role definitions (those live CLI-side; see `concept:role-template`). Adjacent: `concept:permission` (the grant attached to each key), `concept:anonymous-mode` (the data-derived deployment state when no active keys exist), `concept:event-log` (auth audit emissions).

## Invariants

- **Plaintext is surfaced exactly once.** At mint and at each rotation. The server retains only the SHA-256 hash. Lost plaintext is unrecoverable; recovery is rotation.
- **Keys are revoked, not deleted.** A revocation timestamp is set; the row persists. Preserves the audit trail (auth-access audit rows carry the key id and join through to the row).
- **Active-status predicate.** A key is active iff it has not been revoked and neither its expiry nor its scheduled-revoke time has passed. The middleware applies this on every request; the anonymous-mode predicate consults the same definition.
- **Name uniqueness is partial.** The active-name uniqueness index excludes rows in the rotation-grace window so a rotation can mint a new row with the same name while the old one is still active.

## Lifecycle

- **Mint** — the key-mint endpoint. CSPRNG plaintext minted; its SHA-256 hash stored; plaintext surfaced in the response and never persisted. Emits a key-created audit event.
- **Rotate** — the key-rotate endpoint with a grace duration. Atomic: schedules the existing row's revocation at now-plus-grace and inserts a new row with the same name + permissions in one transaction. Old key authenticates normally until the grace expires; the rotation-grace sweep (a periodic scheduler job) then revokes it. Emits a key-rotated audit event, and later a key-revoked event with a rotation-grace reason.
- **Revoke** — the key-revoke endpoint. Sets the revocation timestamp to now. Refuses if the operation would leave zero active keys unless an explicit force-leave-anonymous flag is supplied. Emits a key-revoked audit event with a manual reason.
