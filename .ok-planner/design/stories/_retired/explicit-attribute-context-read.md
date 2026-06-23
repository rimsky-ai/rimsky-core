---
story: explicit-attribute-context-read
status: retired
---

# Template author reads an upstream attribute without firing the receiver

## Retirement note

Retired with the removal of `wake_on_change` from the subscription language. The story's whole capability — "subscribe to an upstream signal but suppress wake on it; still receive the upstream's data into the substitution context via the drained wait-set row" — depended on two mechanisms that no longer exist. The `wake_on_change: false` flag is gone (subscriptions either declare a real wake interest or aren't declared), and substitution-deps no longer read from wait-set rows (`decision:substitution-deps-from-persisted-senders` reads from each subscribed sender's most-recent fresh-settled attribute store directly). Both axes of the user-observable contract this story described are no longer expressible in the platform.

## Original capability (retained for history)

The receiver carries an explicit `subscribes:` entry naming the upstream's signal type (or a wildcard) with `wake_on_change: false`. A matching emission from that upstream inserts a wait-set row carrying the upstream's data into the receiver's substitution context — but does not stale-mark the receiver. The receiver dispatches only when one of its other subscriptions fires it; when it does, its substitution context contains the upstream's value if the upstream settled in the same frame AFTER the receiver was already pulled into the frame.
