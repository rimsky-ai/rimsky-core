---
audit: compose-namespace-guard
artifact: story:compose-namespace-guard
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:59:54Z
---

# Whether the compose namespace is reserved against every other client

Unsupported: on a deployment in the shipped default posture, an ordinary HTTP
client that is not the compose machinery created a compose-prefixed tag, a
compose-prefixed instance key, and a template carrying a compose-prefixed tag,
and all three landed and read back out of the deployment afterwards. The only
thing separating that client from the compose machinery is a request header the
client sets on itself, `X-Rimsky-Compose-Origin`: without it every attempt was
refused, and with it every attempt was accepted. The reservation does hold where
a key is required — with authentication enabled, all three creations were refused
across three client surfaces (the HTTP API, the MCP JSON-RPC surface, and the
CLI), with and without the same spoofed header, and even for an admin key, and
nothing landed. That posture is not an available remedy, though: the compose
verbs send no credential of any kind, so on an authenticated deployment planning
and applying a manifest both fail unauthorized under every key-passing mechanism
the CLI offers, while an ordinary verb with the same key succeeds. There is
therefore no posture in which compose is usable and the namespace is reserved
against other clients, which is the disjointness the story promises. Fourteen
checks passed and five failed across the two postures.
