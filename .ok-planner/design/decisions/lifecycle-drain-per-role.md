---
decision: lifecycle-drain-per-role
---

# Every runtime role drains the lifecycle outbox

## Choice

Each of the three runtime roles runs its own drain over the one lifecycle outbox, and a role kicks its own drain the moment it stages a row, so a delivery follows its transition without waiting for an interval in any deployment. The per-scope advisory lock serialises delivery across drains; the interval is the retry path alone.

## Rationale

A kick is an in-process wake, so it reaches only a drain in the staging process. With one drain in the control-api role, the scheduler's and the supervisor's run-scope terminals wait for the interval in the split deployment — a latency the direct call never had — and the kick helps only the all-in-one deployment. A drain per role keeps the promise that delivery does not wait on the interval in every topology, and the lock that already guards concurrent drains makes the extra drains free of double delivery.

## Alternatives

- One drain in the control-api role — rejected: in the split deployment every staged run-scope terminal lags by up to the interval, and the kick reaches no drain in two of the three roles.
- A cross-process wake through the database — rejected: a notification channel is a second delivery mechanism beside the one the outbox already is, with its own failure modes.
