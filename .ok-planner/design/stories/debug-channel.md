---
story: debug-channel
status: as-is
---

# Operator overrides node state and attribute values in debug mode

## Role

As an operator,

## Capability

I can override-invalidate a specific node or override-set an attribute value via the control-api when the target instance is paused or at a breakpoint pause-mode hit,

## Business value

so that ad-hoc inspection and mutation are available exactly when I have explicitly entered debug mode, and unavailable otherwise.

## Acceptance

With an instance whose instance-level pause flag is true OR with an unresumed pause-mode breakpoint hit blocking a runner, I can post a debug override that stale-marks a specific node and/or sets a specific attribute value in the running frame; the override applies in that frame. When the instance is neither paused nor breakpoint-stopped, the same request is refused with an error citing the required state.

## Falsifier

A debug override is accepted on an instance that is neither paused nor breakpoint-stopped; OR the override is refused on a paused-or-breakpointed instance.

## Proof

Executable proof. Override accepted on both legal states (paused, breakpoint); refused on a healthy running instance with the expected error.
