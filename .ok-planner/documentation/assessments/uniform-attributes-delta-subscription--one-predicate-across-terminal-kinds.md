---
assessment: uniform-attributes-delta-subscription--one-predicate-across-terminal-kinds
subject: story:uniform-attributes-delta-subscription
way: one-predicate-across-terminal-kinds
release: d977250c
outcome: held
warrant: experiment:uniform-attributes-delta-subscription
---
# One subscription on the verdict's attributes, firing on success and on error alike

The audit drove a deployment against a third-party executor that wrote the same verdict attribute alongside a success outcome and alongside an error outcome. Four producer nodes covered both terminal kinds crossed with both verdict values, and four watcher nodes carried the identical subscription — one wildcard terminal path with one condition on the attribute value, with no per-kind entry written anywhere. The two producers whose verdict carried the watched value fired their watchers once each, one on a success terminal and one on an error terminal. The two carrying the other value fired nothing, though all four producers ran, so the silence was the condition's and not a missing signal. The erroring producer's attribute survived its error and reached its watcher, so an author does not go blind to the verdict when the producer fails.

## Unverified remainder

None: the passing run demonstrates the way as promised.
