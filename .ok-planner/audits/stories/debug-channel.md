---
audit: debug-channel
artifact: story:debug-channel
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# Two override actions, open in debug mode and shut outside it

Supported. Both override actions were driven in both of the 2 states the story
names, and refused in the state it excludes. On a running instance in neither
state — mid-frame, with a node parked and no breakpoint installed — both actions
answered 409 naming the two states that would admit them, and the target node's
attribute values were unchanged afterward. Pausing that instance opened both: the
attribute-value override answered with the paused gate and one run mutated, and
the node read back carrying the operator's value; the node-invalidate answered on
the same gate and ran its node again once the instance resumed. On a second
instance held at an unresumed pause-mode breakpoint hit and never paused, both
actions answered on the breakpoint gate with the same effects, and once the hit
was released and the instance settled, the same action answered 409 again.

## Compliance

The body names the delivery surface ("via the control-api"), which the story
rules place in `decisions/` rather than in a story. Compliant text: "As an
operator, I can override-invalidate a specific node or override-set an attribute
value when the target instance is paused or at a breakpoint pause-mode hit, so
that ad-hoc inspection and mutation are available exactly when I have explicitly
entered debug mode, and unavailable otherwise."
