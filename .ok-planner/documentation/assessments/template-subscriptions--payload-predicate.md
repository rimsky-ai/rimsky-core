---
assessment: template-subscriptions--payload-predicate
subject: story:template-subscriptions
way: payload-predicate
release: d977250c
outcome: held
warrant: experiment:template-subscriptions
---
# Filtering on what the arriving event actually contains

The audit ran two nodes whose subscriptions carried a condition on the arriving event's contents: one condition the payload satisfies, one it fails. The satisfied one fired exactly once and the failed one did not fire at all, while the source node demonstrably ran and emitted its signal. Both the path match and the payload condition therefore gate the firing, so an author can write a reactive node that filters precisely on what triggers it instead of waking and checking.

## Unverified remainder

One payload shape and two conditions over it were exercised. The demonstration does not establish what happens when a condition refers to a field the arriving payload does not carry.
