---
assessment: loop-counter-cap--bounded-rounds
subject: story:loop-counter-cap
way: bounded-rounds
release: d977250c
outcome: held
warrant: experiment:loop-counter-cap
---
# Bounding an iteration at a declared number of rounds without writing an executor

The audit registered a template through `catalog:http-routes/POST /v1/templates` whose only node is the bundled counter kind, self-subscribed and filtered on the iteration tag, with no executor of the template author's own anywhere in it, and created instances of it through `catalog:http-routes/POST /v1/instances` against an all-in-one deployment (`catalog:images/rimsky-all-in-one`). At a declared maximum of four the node dispatched exactly four times, emitting counts one through four; at a declared maximum of one it dispatched exactly once. Both instances then came to rest with no live runs, so the iteration stopped at the declared bound rather than merely pausing. The whole loop is expressed in the template, which is the "without authoring a custom executor" half of the promise. Six checks, none failing.

## Unverified remainder

Two bounds were driven, one and four. The way does not establish behaviour at a bound of zero, at a very large bound, or when the counter node is combined with other iteration controls.
