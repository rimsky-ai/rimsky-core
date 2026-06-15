---
concept: invalidate
status: retired
aliases: []
---

# Invalidate

The "sole graph-level message" framing dissolves into the typed-message machinery; every message arrival is structurally an invalidate by virtue of cascade subscribers to the message-virtual-node. The `frame: in | next` discipline retires; cross-frame coupling is expressed by message-emitter nodes. → `concept:message`, `concept:message-schema`, `concept:message-emitter-node`.
