---
decision: one-message-per-frame
status: as-is
---

# One message per frame

## Choice

Every frame carries at most one delivered message. The message-delivery pass at each frame boundary delivers the oldest single pending message; the rest stay pending until the next boundary. N pending messages produce N sequential frames.

## Rationale

Substitution into the message body is always well-defined — a typed message-body read resolves against exactly one body. The "no silent override loss" property holds by construction: two messages binding the same receiver to different values cannot land in the same frame because they cannot share a frame.

## Alternatives

- A `coalesce` frame mode that bundles multiple pending messages into one frame — rejected: a frame-level coalesce switch does several jobs at once (queue-merging plus message-bundling plus cascade-walker re-fire); the queue-shape question is its own per-instance setting (see `decision:message-queue-mode-per-instance`), leaving message-bundling with no use case a message authoring decision does not serve better.
