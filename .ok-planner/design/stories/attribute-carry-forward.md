---
story: attribute-carry-forward
status: as-is
aliases: []
---

# Stateful attribute carry-forward within a RunScope

## Story

As a template author, I can write a node whose executor sets an output attribute and observe that value present in the incoming attribute bag on subsequent dispatches of the same node within the same RunScope (which lives in exactly one frame per `decision:run-scope-is-per-frame`, so this is intra-frame by construction); in a new RunScope — sub-graph invocation, fan-out partition, or a new frame entirely — the same node starts with the schema's defaults, so stateful nodes hold their state in their own attributes uniformly across the platform. Cross-frame state propagation is not provided by carry-forward; it travels through a message body (see `concept:message`).
