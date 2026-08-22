---
decision: sensor-emission-permanent-drop-vs-transient-retry
---

# A sensor drops a permanently rejected message and retries a transient failure

## Choice

A sensor whose message post the control API rejects permanently drops that message, logs the rejection with its subscription and the status, and advances its consumed state exactly as a successful post would. A transient failure — a transport error, a server error, or the request-timeout and rate-limit statuses — leaves consumed state unadvanced, so the sensor re-attempts the observation on its next cycle (see `concept:sensor`).

## Rationale

A permanent rejection means the control API will never accept that message, so retrying it holds the watch on an observation that cannot succeed. Advancing state past it lets the next observation through, and the loud log is what an operator acts on. A transient failure means the same message can succeed later, so holding state re-attempts it on the next cycle. Retrying every rejection forever is not durable either. A newer observation supersedes an older one through the sensor's own deduplication, and a misconfigured watch sits in permanent retry producing nothing.

## Alternatives

- Retry every failure until it succeeds — rejected: a misconfigured watch retries a message the control API always rejects and never observes anything again.
- Drop every failure and always advance — rejected: a restarting control API costs the watch observations a retry would have delivered.
