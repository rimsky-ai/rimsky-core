---
concept: api-key
status: as-is
aliases:
  - bearer token
---

# API key

## What it is

A high-entropy, rimsky-issued credential carried by control-api clients as a bearer token. The plaintext is a high-entropy, prefix-tagged string. The server stores only a one-way hash digest in a persisted API-key ledger; the plaintext is surfaced exactly once at mint and again at each rotation. The mint/hash/validate helpers, the persisted row type, and the per-request middleware lookup together implement the credential.

## Purpose

Rimsky needs an authentication floor: every control-api endpoint should be able to tell who is calling, and operators need a primitive they can mint, rotate, and revoke without redeploying. API keys are the floor — deployments that need richer identity (OIDC, SAML) terminate that at their edge and inject API keys downstream. The ledger is also rimsky's ENTIRE principal registry: there is no user entity, so a human is just the holder of a key's plaintext and a service principal IS an api-key (see `concept:peer-auth`).

## Boundaries

Owns: the plaintext format + hash; the persisted API-key ledger; the lifecycle verbs (mint / list / show / revoke / rotate / sweep); the rotation-grace sweep. Does NOT own: external IdP integration, rate-limiting, role definitions (see `concept:role-template`); the certificate machinery that derives a service's short-lived identity from a `service:enroll`-bearing key (that is `concept:peer-auth` — the api-key is the standing secret, the cert is the derived identity). Adjacent: `concept:permission` (the grant attached to each key, including the `service:enroll` grant that authorizes enrollment), `concept:anonymous-mode` (the data-derived deployment state when no active keys exist), `concept:event-log` (auth audit emissions), `concept:peer-auth` (service principals are api-keys).

## Invariants

- **Plaintext is surfaced exactly once.** At mint and at each rotation. The server retains only a hash digest. Lost plaintext is unrecoverable; recovery is rotation. This one-way-hash storage is the strongest tier of rimsky's graduated secret-at-rest posture (see `decision:secret-at-rest-posture`).
- **Keys are revoked, not deleted.** A revocation timestamp is set; the row persists. Preserves the audit trail (auth-access audit rows carry the key id and join through to the row).
- **Active-status predicate.** A key is active iff it has not been revoked and neither its expiry nor its scheduled-revoke time has passed. The middleware applies this on every request; the anonymous-mode predicate consults the same definition.
- **Name uniqueness is partial.** The uniqueness index on name excludes only rows carrying a revocation timestamp or a scheduled rotation-grace revoke time, not rows whose expiry has passed, so a rotation can mint a new row with the same name while the old one is still valid; an expired-but-unrevoked row keeps blocking its name until it is revoked.
- **A service principal is an api-key.** Under `peer_auth: mtls` an operator-deployed service holds an api-key carrying the `service:enroll` grant; the key is the standing secret that authorizes obtaining a short-lived certificate identity, and revoking the key stops the certificate's renewal so it ages out within its TTL (see `concept:peer-auth`, `concept:permission`).

## Lifecycle

- **Mint** — a fresh plaintext is generated from a cryptographically strong source; its hash digest is stored; the plaintext is surfaced in the response and never persisted. The lifecycle phase emits an audit event.
- **Rotate** — a grace-duration rotation atomically schedules the existing row's revocation at now-plus-grace and inserts a new row with the same name, permissions, and expiry in one transaction. Old key authenticates normally until the grace expires; the rotation-grace sweep (a periodic scheduler job) then revokes it. The phase emits an audit event, and the deferred revocation emits a follow-up audit event distinguishing rotation-grace from manual revocation.
- **Revoke** — sets the revocation timestamp to now. Refuses if the operation would leave zero active keys unless an explicit force-leave-anonymous flag is supplied. Emits an audit event distinguishing the revocation reason.
