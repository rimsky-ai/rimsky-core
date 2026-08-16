---
experiment: template-subscriptions
commit: d977250c
---

# CEL-predicated subscriptions on a canonical signal type-path

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image. One source node
runs the in-process `verifier-shape-checks` executor and emits a
`terminal/success` signal whose payload carries the executor's attribute
delta. Five subscriber nodes sit on that one signal: an exact type-path, a
trailing-wildcard prefix, a non-matching type-path, a CEL predicate over the
payload that holds, and one that does not. `run.sh` boots and removes the
container, taking a free host port unless `PORT` names one.

## What was observed

The template registered with all five subscription forms admitted. The source
ran and emitted its signal. The exact type-path, the trailing-wildcard prefix,
and the node whose CEL predicate the payload satisfies each fired exactly once.
The node subscribed on a different type-path did not fire, and neither did the
node whose CEL predicate the payload fails — the predicate is evaluated against
the arriving signal's payload and gates the firing.

Six checks, none failing.
