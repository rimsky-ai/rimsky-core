---
audit: template-fan-out
artifact: story:template-fan-out
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:39:46Z
---

# A declared fan-out partitions a claim, dispatches concurrently, and settles the parent on the aggregate

Supported: a run through the CLI and control API of an all-in-one deployment,
with the bundled filesystem claim producer over a bind-mounted workspace, drove
three templates against a concurrency-observing endpoint that holds each request
open and reports the peak in flight. The producer's split returned one sub-scope
per declared partition, the dispatch recorded three sub-claims keyed by
partition, and the parent plus its three clones all settled fresh. The endpoint
saw a peak of three in flight and three served, so the work units genuinely
overlapped; the same template with the parallelism knob at one gave a peak of one
and the same three served, which is the control proving the overlap is the
product's dispatch and not the endpoint's. The parent's aggregated settlement
follows the last sub-claim's resolution in the event sequence. With the endpoint
failing every partition, no run settled fresh, the parent settled failed naming
the aggregation verdict, and the partitions' claims were abandoned rather than
committed. Twelve checks, none failing.
