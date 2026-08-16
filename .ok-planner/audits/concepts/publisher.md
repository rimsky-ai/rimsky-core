---
audit: publisher
artifact: concept:publisher
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# The publisher peer-service protocol, its registry entry, and its send-time trust model

Supported. The protocol service declares exactly the four verbs the concept names — a capabilities handshake, subscribe, unsubscribe, and list-subscriptions — and nothing else. Publishers are addressed from the publisher block of the unified config file the same way executors and claim producers are, and config loading refuses an entry whose declared protocol membership omits the publisher protocol. Template registration refuses a publisher entry whose kind the named peer does not advertise in its capabilities handshake, with the same shape as the executor and claim-producer refusals, and refuses one whose message type is not declared in the template's message registry. The subscribe request carries the message type and no receiver-routing field at all — the former routing field is retired and reserved in the wire definition — so delivery is left to route by message type against node-subscription edges, and each of the four bundled publisher implementations copies the subscribed type onto every envelope it sends. Send-time trust works as claimed: a request marked as a publisher send must present a subscription id, and rimsky resolves the publisher name from that row and ignores the sender the request declares. The per-instance broadcaster claim holds in the implementations — each bundled publisher keeps one watch per subscription in a single process serving many instances. Mounting-to-active reconciliation is delegated, as the concept says, and the single-replica posture matches the replica concept. Coverage comes from the template-validator gap suite, the runtime publisher lifecycle suite, and four end-to-end sensor scenarios against a live stack.
