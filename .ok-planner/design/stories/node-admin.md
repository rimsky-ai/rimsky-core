---
story: node-admin
status: as-is
---

# Operator inspects and resets nodes

## Role

As an operator, I can inspect a node's full state on a running instance and reset a failed node's error counter, so that I drive an errored node back into the cascade without re-instantiating the whole instance.

## Capability

Operator-driven node administration: inspect state, reset error counter, both through the control-api or CLI.

## Business value

Operators drive an errored node back into the cascade without re-instantiating, and observe what state the node is in to inform that decision. Driving a healthy-but-stalled node back through the cascade is a different operator workflow — sending a typed message the template declares for that purpose, via the universal message-emit surface (`story:message-schema`, `story:message-bus`). Force-stale on a paused or breakpointed instance lives at the debug-override surface (`story:debug-channel`).

## Acceptance

Through the control-api or the node-admin CLI surface, an operator retrieves a node and sees its current state and settling signal type; force-invalidating a node causes the supervisor to re-fire it on a real dispatch; invalidating with the in-cascade option joins the running cascade frame and the node settles inside that frame rather than the next one; resetting a failed node clears its error count and the next acquisition attempt is not skipped due to error budget exhaustion.

## Falsifier

Reset clears the visible counter but the supervisor still treats the node as exhausted.

## Proof

Executable proof.
