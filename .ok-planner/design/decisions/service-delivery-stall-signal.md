---
decision: service-delivery-stall-signal
---

# A stalled service delivery is an event-log edge pair and a diagnostics route

## Choice

Both retry loops that deliver to services — the lifecycle outbox and the producer-verb outbox — persist their failure state on the outbox row: the attempt count, the time of the next attempt, and the last error. A service is stalled when the oldest pending row in any of its streams has waited longer than one deployment-wide duration, `service_delivery.stall_after`, which also caps the retry backoff, so the drain retries a stalled service no less often than the threshold that declares it stalled. Rimsky writes one event-log entry when a service's delivery first stalls and one when it next succeeds; the two kinds join the closed operational set (see `decision:event-log-kind-enum`). An operator reads what is owed right now from a diagnostics route per outbox, which lists each service's pending rows, their age, their attempts, and their last error.

## Rationale

`concept:event-log` promises the operator a record they ask instead of reconstructing history from process output, and "when did this service stall" is exactly such a question. An entry per failed attempt would write tens of thousands of rows a day against one dead subscriber and carry nothing new, so the signal is the edge, not the attempt. The route alone answers the present and not the past; the edge pair alone answers the past and not the present; together they cover both. One threshold serves both loops because the ruling asks the signal to cover service delivery generally, and age rather than attempt count defines it because backoff makes an attempt count mean a different elapsed time in each loop.

## Alternatives

- An event-log entry per failed attempt — rejected: volume without information.
- A diagnostics route alone — rejected: leaves the event log's promise unmet for this class of failure.
- Declare service-delivery health out of the event log's scope — rejected: it narrows a live concept's purpose to avoid two enum values.
- A fixed threshold constant — rejected: a deployment's tolerance for a slow service is its own.
