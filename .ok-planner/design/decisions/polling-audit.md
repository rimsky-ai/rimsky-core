---
decision: polling-audit
status: as-is
---

# Event-driven waits where polling masks ordering

## Choice

Test sites that wait by sleep or deadline polling are audited: genuine outcome-waits stay as polls; the subset whose polling masks an ordering assumption is converted to event-log-tail waits on the durable record of the transition.

## Rationale

Waiting on the durable record of a transition cannot miss or race the sampler; flaky-under-load tests erode the gate exactly when it is the consolidation's net.
