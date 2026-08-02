---
audit: compose-driver-sends-empty-message-after-create
artifact: decision:compose-driver-sends-empty-message-after-create
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:22Z
---

# Auto-wake after create, gated on structural-root presence, keyed for idempotency

Supported. After each instance-create in `compose run`'s apply step, the driver inspects the created instance's template (caching the per-template-hash result across repeated instances of the same template within one run) and sends an empty message only when the template has at least one structural root, using a wake key deterministically derived from the instance's key; a template with no structural root is skipped entirely. The structural-root inspection helper carries its own unit tests for both a rooted and a non-rooted template plus error-propagation cases, and the server's general idempotency-key dedup mechanism (a duplicate key on `POST /instances/{id}/messages` replays rather than double-sends) is what the deterministic key relies on for retry safety; the same wake step is reused verbatim by the self-hosted `rimsky run` verb and the remote dev-loop `rimsky run` path.
