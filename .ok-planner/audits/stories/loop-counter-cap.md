---
audit: loop-counter-cap
artifact: story:loop-counter-cap
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:08Z
---

# The bundled counter node bounds iteration and marks its last round

Supported: a run through the control API of an all-in-one deployment drove a
template whose only node is the bundled counter kind, self-subscribed and
filtered on the iteration tag, with no executor of the author's own anywhere in
the template. At a maximum of four the node dispatched four times, emitting
counts one through four, the first three carrying the iteration tag and the
fourth the terminal one. At a maximum of one it dispatched once, carrying the
terminal tag on that single round. Both instances came to rest with no live runs,
so iteration stopped at the cap in each case. Six checks, none failing.

## Compliance

Prescribes mechanism by naming the node kind, its maximum-count input attribute, and the two literal tag strings — all owned by the decision that fixes the counter's shape; the compliant text says the author can bound an iteration at a declared number of rounds and tell which round is the last, without authoring an executor.
