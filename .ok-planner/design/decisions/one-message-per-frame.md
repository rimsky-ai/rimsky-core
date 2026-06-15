---
decision: one-message-per-frame
status: as-is
---

# One message per frame

## Choice

Every frame carries at most one delivered message. The message-delivery pass at each frame boundary delivers the oldest single pending message; the rest stay pending until the next boundary. N pending messages produce N sequential frames.

## Rationale

Substitution into the message body is always well-defined — a typed message-body read resolves against exactly one body. The "no silent override loss" property the prior coalesce mechanism maintained by hand is satisfied here by construction: two messages binding the same receiver to different values cannot land in the same frame because they cannot share a frame.

## Alternatives considered

Keep `coalesce` as a non-default opt-in mode for instances that explicitly want it. Rejected: coalesce-as-frame-mode does multiple jobs (queue-merging plus message-bundling plus cascade-walker re-fire); splitting them to keep only the queue-merging job leaves one narrow use case better served by message authoring decisions.
