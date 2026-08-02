---
audit: secret-at-rest-posture
artifact: decision:secret-at-rest-posture
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:16Z
---

# Secret at-rest protection is graduated: hashed api-keys, app-encrypted CA key, delegated config-blob secrets

Supported. Api-keys: `persistence.APIKey` (`lib/foundation/persistence/api_keys.go`) carries only `KeyHash`, no plaintext column — mint/rotate compute a one-way SHA-256 digest (`lib/foundation/auth/plaintext.go`) and the plaintext is returned only in the mint/rotate HTTP responses. CA key: `lib/foundation/pki/encryption.go::EncryptCAKey`/`DecryptCAKey` use AES-256-GCM (32-byte key) keyed by `RIMSKY_CA_ENCRYPTION_KEY`, and `lib/control/config/peer_auth.go::ensureDeploymentCA` / `claim_producers.go` fail closed at startup (a wrapped, non-nil error before any listener starts) whenever `peer_auth: mtls` is set and the env key is unset or malformed. Config-blob secrets: the bundled webhook sensor (`lib/services/sensors/sensor-webhook/sensor.go`) persists only a per-subscription watermark in its own state store and receives the full resolved config, secret included, fresh from every subscribe/resync call — no local secret copy. The publisher-subscription instance DTO (`lib/control/controlapi/instances.go::instanceSubscriptionItem`) is an explicit allowlist of fields (id, publisher name, kind, message type, state, started-at, failure reason) that excludes the resolved-config blob, so the secret is never returned over the API; a repository-wide check of every `ResolvedConfig`-carrying call site (16 non-test occurrences across `lib/runtime` and the four bundled sensors) found no logger call touching it.
