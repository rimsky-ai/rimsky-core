---
decision: single-frame-creation-path
status: as-is
---

# Single frame-creation path

## Choice

A frame opens only when a message lands in the ledger and the next frame boundary picks it up. The cascade walker has no path that creates a frame.

## Rationale

Every frame's origin is auditable via a single mechanism (a triggering-message-id column on the frame row): "why did this frame open" is always answerable from the observability surface. Cross-frame coupling is explicit at the sender (a message-sender node), not hidden under cascade-walker behavior.

## Alternatives

- A per-subscription "next frame" modifier as a second frame-creation path — rejected: the dual path is exactly the source of the multi-node back-edge silent-failure footgun (a downstream sender's settle does not re-dispatch the upstream receiver in a multi-node cycle), with the affordance buried in the cascade walker's discipline instead of visible at the sender.
