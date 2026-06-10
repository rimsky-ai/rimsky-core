---
story: node-admin
status: as-is
---

# Operator inspects and admin-invalidates nodes

## Role

As an operator, I can inspect a node's full state on a running instance, force it stale to re-fire it, target the invalidate at either a freshly-enqueued frame or the cascade frame currently running, and reset a failed node's error counter, so that I drive a stalled or errored node back into the cascade without re-instantiating.

## Capability

Operator-driven node administration: inspect state, force-invalidate (next frame or in-cascade), reset error counter, all through the control-api or CLI.

## Business value

Operators drive a stalled or errored node back into the cascade without re-instantiating the whole instance, and observe what state the node is in to inform that decision.

## Acceptance

Through the control-api or `rimsky admin …` CLI, an operator retrieves a node and sees its current state and settling signal type; force-invalidating a node causes the supervisor to re-fire it on a real dispatch; invalidating with the in-cascade option joins the running cascade frame and the node settles inside that frame rather than the next one; resetting a failed node clears its error count and the next acquisition attempt is not skipped due to error budget exhaustion.

## Falsifier

Invalidate flips state but the supervisor never picks the node up, OR the in-cascade option produces a separate frame rather than joining the running one, OR reset clears the visible counter but the supervisor still treats the node as exhausted.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
