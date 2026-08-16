---
assessment: executor-protocol--declared-tags
subject: story:executor-protocol
way: declared-tags
release: d977250c
outcome: held
warrant: experiment:executor-protocol
---
# Having the tags my executor declares steer subscriptions at run time

The tags the peer advertised at discovery proved load-bearing while the graph ran, not merely descriptive. Two subscribers filtered on different declared tags of the same sending node: the one matching the tag the peer actually emitted ran, and the other never fired. Together with registration-time validation — a subscription filtering on a tag the peer never declared is refused — the declared tag set is both the vocabulary template authors may filter on and the thing that decides which downstream node runs. A service author therefore controls downstream routing by what the executor declares and emits.

## Unverified remainder

None: the passing run demonstrates the way as promised.
