---
audit: template-fan-out
artifact: story:template-fan-out
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# A fan-out declaration partitions a claim and runs one work unit per sub-claim

Supported. Against an all-in-one deployment with the bundled filesystem claim
producer configured, a single node declaring a claim, a 3-element partition
request, a parallelism cap and an aggregation policy produced exactly what the
story promises: the producer's split returned 3 sub-scopes, the dispatch recorded
3 sub-claims keyed by partition, and all 4 runs — the parent and its 3 clones —
settled fresh. Concurrency was measured rather than inferred: the work unit was
pointed at an endpoint that holds each request open and reports the peak number
in flight, which read 3 of 3 under a parallelism of 3 and 1 of 3 under a
parallelism of 1. The parent's aggregated settlement followed the last
sub-claim's resolution in event order, and with every partition failing the
parent settled failed under its declared aggregation verdict with the partitions'
claims abandoned.
