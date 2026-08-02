---
audit: openlineage-outbound-bearer
artifact: decision:openlineage-outbound-bearer
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:57Z
---

# OpenLineage subscriber can present an optional outbound bearer token

Supported. `lib/services/subscribers/openlineage/config.go` reads an optional `RIMSKY_OPENLINEAGE_BEARER_TOKEN` (empty allowed, no validation forcing it); `emitter.go::Emitter.Send` (annotated `@decision: openlineage-outbound-bearer`) sets `Authorization: Bearer <token>` only when the token is non-empty, otherwise omits the header. Both branches are directly tested: `TestEmitter_AddsBearerTokenWhenConfigured` asserts the header is set to the configured value, and `TestEmitter_NoBearerTokenNoAuthHeader` asserts no header is sent when unset.
