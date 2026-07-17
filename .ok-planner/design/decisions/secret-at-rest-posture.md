---
decision: secret-at-rest-posture
status: as-is
aliases:
  - secrets-at-rest
  - config-secret-delegation
---

# Secrets at rest are protected in graduated tiers by value

## Choice

Rimsky's at-rest protection for a stored secret is graduated by the secret's value and blast radius:

- **Api-keys** are stored as one-way hash digests, never as recoverable plaintext (see `concept:api-key`).
- **The deployment CA private key** is app-level encrypted with AES-256-GCM, keyed by `RIMSKY_CA_ENCRYPTION_KEY`, and fail-closed at startup when the mode is on and the key is missing or malformed. It is the crown-jewel root of the mTLS trust chain, so it carries the strongest protection rimsky applies (see `concept:peer-auth`, `decision:peer-auth-mtls`).
- **Secrets embedded in a publisher-subscription resolved-config blob** — e.g. a webhook sensor's HMAC shared secret — are NOT app-level encrypted. Their at-rest protection is delegated to the operator's deployment controls: infrastructure encryption-at-rest (volume/disk encryption), restricted database access, and encrypted backups. Rimsky additionally guarantees these secrets are never logged and never returned over any API surface (see `concept:publisher-subscription`).

## Rationale

App-level field encryption and infrastructure encryption-at-rest defend different layers. Infra encryption-at-rest protects the physical/storage layer — a stolen disk or backup volume is unreadable — but it is transparent to any authorized database connection, so it does nothing to protect a credential against a DB-query, dump, or injection path that reaches the row through a live connection. App-level encryption is the control that keeps the plaintext unreadable even to an authorized connection, because the decrypting key lives outside the database.

That distinction makes the graduated posture proportionate. The CA private key warrants app-level encryption: it is a single high-value secret whose compromise forges the entire internal trust fabric, and its key material (`RIMSKY_CA_ENCRYPTION_KEY`) is already a required part of the mtls posture. A config-blob secret is lower-value and bounded in blast radius — it authenticates one publisher-subscription binding, is rotatable at the operator's substrate, and its compromise does not cascade beyond that binding. Delegating its at-rest protection to operator controls, while guaranteeing it never leaks through logs or the API, matches the protection to the risk without spending a per-field encryption/key-management mechanism on every config blob.

The delegation is also the sole at-rest custodian for a webhook secret: the bundled webhook sensor no longer persists the secret in its own state DB, keeping only its watermark and re-provisioning the full config — secret included — through subscription resync. The only at-rest copy of a webhook secret is therefore rimsky's resolved-config blob, which this posture governs.

## Alternatives

- **App-level-encrypt all config secrets now** — rejected as disproportionate for current scope. A general config-field encryption mechanism (encrypting every secret-bearing field of every resolved-config blob) requires its own key posture, rotation story, and key-availability failure modes, for secrets whose blast radius is a single binding. It is a plausible future hardening, not a present requirement; reserving app-level encryption for the crown-jewel CA key keeps the mechanism where the value justifies it.
- **Rely on infrastructure encryption-at-rest alone (for all secrets, including the CA key)** — rejected. Infra encryption-at-rest is transparent to authorized DB connections, so it does not by itself protect a credential against query/dump/injection access to the row — only against physical media theft. It is a sufficient floor for a bounded-blast-radius config secret paired with restricted DB access, but it is not sufficient for the CA private key, whose compromise forges the whole mTLS trust chain; that key needs the app-level encryption that keeps it unreadable to a live database connection.
