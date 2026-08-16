---
audit: upstream-gating-at-eligibility
artifact: decision:upstream-gating-at-eligibility
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:39:39Z
---

# The condition is a predicate on the eligibility surface, not per-walk seeding

Supported. The candidate-selection query is the dispatch-eligibility surface, and in both persistence drivers it carries the same predicate set: the row must be stale and unclaimed on a live instance, no sibling run of the same node in the same scope may be claimed, held, or parked, and no undrained wait-set row may name the row as receiver. Alongside it the gate evaluator refuses to make a receiver eligible at all while any subscribed upstream has an in-flight run in the same frame — one probe that resolves the receiver's senders from the template's subscription-edge map and asks the queue for their in-flight states in the frame and scope, with the receiver's own node id excluded so a self-edge never blocks itself. Neither check is seeded per walk: both are conditions evaluated where eligibility is decided, which is the chokepoint property the decision argues for. The wait-set keeps its drained-rows substitution role, supplying pinned sender-run identities to the substitution builder. The deterministic diamond is pinned by a scenario test carrying the story annotation, and a self-edge carry-forward scenario exercises the cycle idiom under the same predicate.
