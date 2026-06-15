---
decision: single-frame-creation-path
status: as-is
---

# Single frame-creation path

## Choice

A frame opens only when a message lands in the ledger and the next frame boundary picks it up. The cascade walker has no path that creates a frame; the in-walker frame-creation branch and the helper it called are removed entirely.

## Rationale

Every frame's origin becomes auditable via a single mechanism (a triggering-message-id column on the frame row). "Why did this frame open" is always answerable from the observability surface. Cross-frame coupling becomes explicit at the sender (a message-emitter node), not hidden under cascade-walker behavior.

## Alternatives considered

Preserve a per-subscription "next frame" modifier as a second frame-creation path. Rejected: the multi-node back-edge silent-failure footgun stems precisely from this dual path — a downstream sender's settle does not re-dispatch the upstream receiver in a multi-node cycle, and the documented affordance is buried in the cascade-walker's discipline; keeping the dual path perpetuates the failure mode.
