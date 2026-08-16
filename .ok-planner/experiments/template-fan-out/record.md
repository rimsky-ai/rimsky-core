---
experiment: template-fan-out
commit: PENDING
---

# Fan-out partitioning, concurrency, and parent settlement

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image with the bundled
filesystem claim producer configured over a bind-mounted workspace, so the
node's claim resolves against a producer advertising split-scope support. The
fan-out work unit is the `verifier-http` executor pointed at a
concurrency-observing HTTP endpoint the probe runs on the host: it holds each
request open and reports the peak number in flight at once. `run.sh` starts the
endpoint, boots the container, and removes both on exit. Both host ports default
to free ones, and the three templates are materialized into the run's workspace
with the endpoint's actual port substituted in, so the port the endpoint listens
on and the port the templates dial cannot drift apart.

## What was observed

The producer's split returned one sub-scope per declared partition
(`sub_scope_descriptor_count: 3`), and the dispatch recorded three sub-claims
with the partition keys `p1`, `p2`, `p3`. All four runs — the parent and its
three clones — settled fresh. The host endpoint reported a peak of three
requests in flight and three served, so the work units ran concurrently rather
than one after another; the same template with `parallelism: 1` produced a peak
of one with the same three served, confirming the concurrency observed is
rimsky's dispatch. The parent's aggregated settlement followed the last
sub-claim's resolution in the event log. With the endpoint answering 500 for
every partition, no run of the fan-out settled fresh, the parent settled
failed, the event log carried `terminal/error/aggregate/strict_failed`, and the
partitions' claims were abandoned rather than committed. The number of clones
that settle failed varies between runs under the `strict` policy, which
force-cancels the remaining clones the moment the verdict is decided.

Twelve checks, none failing.
