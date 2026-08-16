---
audit: debug-channel
artifact: story:debug-channel
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:51:57Z
---

# Node and attribute overrides open exactly in the two debug states, and are shut otherwise

Supported. Both states the story names were driven through the public surface as
two ways against released-image stacks, sixteen checks between them, none
failing. In the paused way, an instance running with a node parked refused both
override actions with a conflict naming the two states that would open the
channel, and the node's attribute values were unchanged afterwards; once paused,
the attribute override answered with the paused gate state and one run mutated
and the node read back the operator's value, the node override answered on the
same open channel, no work ran while the instance stayed paused, and resuming ran
the invalidated node again. In the breakpoint way, an instance never paused but
sitting at an unresumed pause-mode hit accepted both overrides with the
breakpoint gate state, the value read back off the node under inspection, and the
invalidated node ran again once the hit was released; after the hit was released,
the breakpoint deleted and the instance settled, the same override was refused
again with the same two states. So the channel is open exactly in the two debug
states and shut on either side of them.

## Compliance

- The body names the delivery surface — "via the control-api" pins the capability to one surface, which decisions own; the compliant capability drops it, leaving the override actions and the states that gate them.
