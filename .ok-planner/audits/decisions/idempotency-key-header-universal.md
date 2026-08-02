---
audit: idempotency-key-header-universal
artifact: decision:idempotency-key-header-universal
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:14Z
---

# Mandatory Idempotency-Key header on the message-send endpoint

Supported. `lib/control/controlapi/messages.go::handleCreateMessage` — the sole handler registered for `POST /v1/instances/{id}/messages`, the single route the control API exposes for creating a message (checked against the route registrations in `messages.go`; both operator and publisher sends flow through this one handler, distinguished only by a body field) — reads the `Idempotency-Key` header and returns a 400 bad-request when it is empty or absent, before any dedup lookup or send occurs. `TestCreateMessage_MissingIdempotencyKeyRejected` in `lib/control/controlapi/messages_test.go` directly exercises this rejection, and every other message-send test in that file supplies the header, consistent with it being mandatory rather than optional.
