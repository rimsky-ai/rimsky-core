---
audit: frame-origin-audit
artifact: story:frame-origin-audit
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:34Z
---

# Operator sees the triggering message for every frame

Supported. The existing frame observability surface
(`GET /instances/{id}/frames` and `GET /instances/{id}/frames/{frame_id}`,
annotated `@story: frame-origin-audit`) joins each frame to its triggering
message and returns `message_type`, `message_sender`, and
`message_sender_kind`. `sender_kind` is a NOT-NULL column on the messages
table constrained by a database CHECK to exactly the 3 values
`operator | publisher | instance` (the "cascade-sent" case is the
runtime's own `instance`-origin sends, per the message-sender-kind
constant set in `lib/runtime/message_delivery.go`), and every frame's
`triggering_message_id` is a NOT-NULL foreign key, so every frame carries
one of the three origins by construction. Route tests confirm every listed
frame carries `triggering_message_id` and that the single-frame endpoint
joins through to `message_sender_kind`; the join query itself pulls the
column generically (not filtered by value), so the publisher and
cascade-sent (`instance`) cases flow through the same code path exercised
for the operator case.
