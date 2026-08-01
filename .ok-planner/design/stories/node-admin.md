---
story: node-admin
status: as-is
---

# Operator inspects and resets nodes

## Role

As an operator, I can inspect a node's full state on a running instance and clear the retry budget of a failed-terminal node, so that I restore acquisition eligibility for a subsequent invalidate-via-message.

## Capability

Operator-driven node administration: inspect state, clear retry budget on failed-terminal node, both through the control-api or CLI.

## Business value

Operators restore an errored node's acquisition eligibility so a subsequent invalidate-via-message can re-attempt dispatch, and observe what state the node is in to inform that decision. Driving a healthy-but-stalled node back through the cascade is a different operator workflow — sending a typed message the template declares for that purpose, via the universal message-send surface (`story:message-schema`, `story:message-bus`), or the empty-message trigger (`story:empty-message-wakes-roots`). Force-stale on a paused or breakpointed instance lives at the debug-override surface (`story:debug-channel`).

