---
decision: polling-audit
---

# Event-driven waits where polling masks ordering

## Choice

Test waits divide by what they wait on: a genuine outcome-wait may poll; a wait whose pass depends on an ordering assumption blocks on the event-log tail — the durable record of the transition — instead.

## Rationale

Waiting on the durable record of a transition cannot miss or race the sampler; deadline polling over an ordering assumption yields flaky-under-load tests that erode the gate exactly when it is the safety net.

## Alternatives

- Deadline polling everywhere — rejected: a poll can sample around an ordering transition, making the verdict load-dependent.
