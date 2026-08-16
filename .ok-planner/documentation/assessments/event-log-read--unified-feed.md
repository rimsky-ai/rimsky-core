---
assessment: event-log-read--unified-feed
subject: story:event-log-read
way: unified-feed
release: d977250c
outcome: held
warrant: experiment:event-log-read
---
# Reading one instance's whole history from a single chronological feed

One instance on a container of `catalog:images/rimsky-all-in-one` was made to produce all four kinds of activity the promise names, and the whole run was then reconstructed from `catalog:http-routes/GET /v1/events`. The feed carried all four: node lifecycle as `catalog:event-kinds/work_started` and the node's terminal signals, two `catalog:event-kinds/breakpoint.hit` rows from an armed breakpoint, two message deliveries — the one the graph sent and the one the operator posted, with the posted payload's value on the record — and the supervisor's own bookkeeping as `catalog:event-kinds/attributes_substituted` and `catalog:event-kinds/work_completed` carrying the supervisor id. The order was the clock's: the feed came back in sequence order, the sequence agreed with the timestamps, and the kinds interleaved rather than grouping, which a kind-grouped listing cannot produce. A malformed instance id was refused rather than answered with an empty feed. Twenty-eight checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
