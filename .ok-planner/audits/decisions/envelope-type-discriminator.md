---
audit: envelope-type-discriminator
artifact: decision:envelope-type-discriminator
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:34Z
---

# Envelope type discriminator

Supported. The message envelope's wire and DB discriminator is `type`
throughout: `postMessageRequest.Type` and `messageItem.Type` in
`lib/control/controlapi/messages.go` (the send/list/get surfaces, the
former annotated `@decision: envelope-type-discriminator`) both carry the
JSON tag `type`, matching the `type` column on the messages table. The
publisher-subscription counterpart field is `message_type` in
`publisher.proto`'s `PublisherSubscriptionDescriptor`/`ListSubscriptions`
messages and the `rimsky_publisher_subscriptions` schema, whose migration
comment records it was "renamed from message_kind" — i.e. the alternative
(a separate `kind` field) was tried and removed. No `kind` field exists
alongside `type`/`message_type` anywhere in the message send/list/publisher
surfaces checked.
