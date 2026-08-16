---
assessment: named-lock-metric--acquisition-counter
subject: story:named-lock-metric
way: acquisition-counter
release: d977250c
outcome: held
warrant: experiment:named-lock-metric
---
# Named-lock acquisitions counted on the same metric as producer-claim acquisitions

The audit opened a released stack's metrics listener (`catalog:env-vars/RIMSKY_METRICS_HOST`, `catalog:env-vars/RIMSKY_METRICS_PORT`) and ran one instance in which three nodes contend for a named lock of limit one while a fourth takes a claim from the bundled filesystem claim producer (`catalog:images/rimsky-claim-producer-filesystem`). The scrape returned a single counter family whose help text carries both acquirer kinds as label values: the named lock at three acquisitions, one per holder, beside the producer at one. The lock appears on its own labelled series, so an operator graphs both acquirer kinds off one metric rather than hunting in separate places. Nine checks across this way and its sibling, none failing.

## Unverified remainder

One named lock and one producer were on the deployment. The way does not establish the metric's shape across many named locks or many producers at once.
