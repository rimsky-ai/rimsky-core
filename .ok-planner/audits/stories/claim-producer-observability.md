---
audit: claim-producer-observability
artifact: story:claim-producer-observability
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:51:57Z
---

# A dashboard reads producer-side claim state off the producer itself

Supported. Driven through the public surface against the released filesystem
claim producer running as its own container over a seeded workspace, with a
dashboard-shaped gRPC client built for the run, and a released-image stack
pointed at the same producer. Thirty-four checks, none failing. All four
capabilities the story names answered: a claim's full detail came back open with
its scope, its opened-at time and its event history; a stream opened on that
claim replayed its state and then carried the commit as a live event while open,
after which the claim read committed; the inventory paginated, a request for two
returning two and a cursor, the next page repeating none of them and the walk
reaching every open claim; and both admin views the producer declares rendered,
each with a column schema and a render hint, the parameterised one listing the
seeded items for its required parameter, while an undeclared view name was
refused rather than fabricated. The control API's entry for the producer
reported it reachable and carried the same three capability flags and both view
declarations, so the dashboard discovers what to render from the control API and
reads the data from the producer — the story's "without writing a custom
backplane".
