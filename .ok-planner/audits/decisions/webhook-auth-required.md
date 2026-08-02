---
audit: webhook-auth-required
artifact: decision:webhook-auth-required
determination: supported
commit: b767a27d
audited: 2026-08-02T09:29:02Z
---

# The webhook sensor requires per-subscription auth, fail-loud

Supported. `sensor-webhook/sensor.go`'s `validateAuthConfig` (tagged `@decision: webhook-auth-required`) rejects a `Subscribe` whose `resolved_config.auth` is nil or whose `mode` is empty, and accepts exactly the three modes `hmac` (secret plus mandatory `timestamp_header` for replay protection, `authenticateHMAC` verifying an HMAC-SHA256 signature over `timestamp.body` and a bounded replay window), `secret_header` (constant-time header compare via `subtle.ConstantTimeCompare`), and the explicit `none` opt-out — any other mode string is a bind-time error. `TestSubscribe_RefusedWhenAuthOmitted`, `TestSubscribe_RefusedWhenAuthModeUnknown`, `TestSubscribe_HMAC_RefusedWithoutTimestampHeader`, `TestServeWebhook_HMAC_AcceptsSignedRejectsUnsigned`, `TestServeWebhook_HMAC_ReplayWindowRejectsStale`, `TestServeWebhook_SecretHeader_AcceptReject`, and `TestServeWebhook_NoneAccepts` cover the fail-loud bind-time gate and all three runtime auth paths.
