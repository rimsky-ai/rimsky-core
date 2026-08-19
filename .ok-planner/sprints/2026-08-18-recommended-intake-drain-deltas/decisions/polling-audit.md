---
decision: polling-audit
---

# Event-driven waits where polling masks ordering

## Choice

Test waits divide by what they wait on: a genuine outcome-wait may poll; a wait whose pass depends on an ordering assumption blocks on the event-log tail — the durable record of the transition — instead. Every wait the wall-clock lint admits carries a marker naming its class. The lint rejects a wait whose marker names the ordering-dependent class. The lint rejects a wait that carries no marker. No wait in the suites is unclassified.

## Rationale

Waiting on the durable record of a transition cannot miss or race the sampler; deadline polling over an ordering assumption yields flaky-under-load tests that erode the gate exactly when it is the safety net. The marker makes the division checkable. It tells a legitimate outcome-poll from an ordering wait. It lets the lint in `decision:test-wallclock-lint-ratchet` reject the second class at the site.

## Alternatives

- Deadline polling everywhere — rejected: a poll can sample around an ordering transition, making the verdict load-dependent.
- The division as prose alone — rejected: no tool can tell the two classes apart, so nothing enforces the rule and converted sites drift back.
