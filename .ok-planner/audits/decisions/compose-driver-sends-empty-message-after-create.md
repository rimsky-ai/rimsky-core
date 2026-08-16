---
audit: compose-driver-sends-empty-message-after-create
artifact: decision:compose-driver-sends-empty-message-after-create
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The post-create wake message, its structural-root condition, and its idempotency key read against the compose driver

Supported. After applying its plan, the compose one-shot walks the instances it created, asks per distinct template hash whether that template declares a structural root — computed by building the template's subscription edges and testing whether anything matches the empty-source terminal-success signal — and sends an empty-bodied instance message only for the instances whose template has one, skipping the rest exactly as the choice says. The idempotency key is deterministic and derived from the instance key by a fixed prefix, so a retried create-plus-wake collapses. Neither rejected alternative is present: no manifest field authors wake messages, and instance-create carries no bundling flag. The ephemeral single-template run applies the same rule with its own deterministic key. A scenario test drives the one-shot to terminal end to end, and the root-detection function has its own unit tests over rooted, unrooted, empty, and malformed specs.
