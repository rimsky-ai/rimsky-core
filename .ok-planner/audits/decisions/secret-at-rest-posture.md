---
audit: secret-at-rest-posture
artifact: decision:secret-at-rest-posture
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:49:58Z
---

# Graduated at-rest protection across the three secret tiers the decision names

Supported. The decision names exactly three tiers and each is carried. Api-keys persist only a SHA-256 digest — the minting helper returns the plaintext once and the persisted row and both backend schemas carry a hash column and no plaintext column, so no recovery path exists. The deployment CA private key is sealed with AES-256-GCM under a 32-byte deployment-supplied key parsed from the environment; config load refuses to start when the peer-auth mode is mTLS and that key is unset, non-base64, or the wrong length, and the parser has unit coverage for all three malformed shapes plus a config-load test asserting the mTLS-without-key startup failure names the variable. Publisher-subscription resolved-config blobs are stored as plain JSON in both backends, with no app-level encryption anywhere — matching the delegation the decision states rather than contradicting it. The two guarantees attached to that delegation hold structurally: the instance-detail response type carries only subscription identity, kind, message type, state, and failure reason, so no API route can return a resolved config, and no log site anywhere in the tree emits one. The webhook-sensor claim also holds — its own state table has exactly three columns (subscription id, last idempotency key, last seen at), the config arrives in memory through subscription resync, and a test drives a secret-header delivery end to end and asserts both that the secret is absent from every state row and that the table's column set is unchanged.
