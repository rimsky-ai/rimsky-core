---
audit: loop-counter-shape
artifact: decision:loop-counter-shape
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:44Z
---

# Loop-counter utility is a carry-forward counter with two declared success tags

Supported. The bundled loop-counter kind declares an attributes schema whose only required property is a strictly-positive maximum and whose count property is marked read-only, and the template validator treats an executor's read-only declaration as authoritative over the node's. Its handler reads the incoming count, increments it, returns the new value in the Success attributes delta, and attaches exactly one of two declared tags — the step tag while the new count is below the maximum, the done tag at and past it — with all four tag-boundary cases plus the seven schema violations covered by unit tests and an end-to-end scenario that drives a self-subscribing counter to three dispatches in one frame and confirms two step-tagged and one done-tagged emission. Carry-forward is read from the prior run in the same run scope, and a new run scope is minted for every frame, every sub-graph invocation, and every fan-out partition, so the reset boundaries the decision names hold by construction. The decision names three alternatives it rejects and no runtime-owned iteration counter exists in the cascade walker.
