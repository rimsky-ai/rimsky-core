---
assessment: named-lock-metric--contention-series
subject: story:named-lock-metric
way: contention-series
release: d977250c
outcome: held
warrant: experiment:named-lock-metric
---
# Lock saturation readable as its own series rather than reconstructed from events

Because the audited run deliberately made lock holders slow enough that the other contenders queue, the deployment had real contention to report. The scrape returned contention as its own labelled series, at twenty, distinct from the acquisition count. That is the saturation signal the story asks to graph and alert on: an operator reads it directly from the metrics surface instead of reconstructing waiting from the event log.

## Unverified remainder

Contention was produced by three holders on one lock of limit one. The way does not establish what the series reports for locks of larger limit or under sustained load, and no alerting rule was exercised.
