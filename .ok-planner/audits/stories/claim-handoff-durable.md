---
audit: claim-handoff-durable
artifact: story:claim-handoff-durable
determination: unsupported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:50:00Z
---

# A durable claim survives its dispatch, but neither of its two later promises holds

Unsupported. Driven through the public surface against a producer that logs
every verb it receives, the first half holds: an acquirer's durable claim handle
records durable lifetime and committed state, a co-holder in that same dispatch
reads the claim by alias and settles fresh, exactly one claim-handle row exists
for the scope, and a competing instance is refused, so the producer still
occupies the scope. Two of the story's promises did not happen. A later dispatch
of the same instance did not co-hold the same row: waking the instance
re-dispatched the acquirer, which opened a second claim, leaving two committed
durable rows for one scope — and no public path dispatches the co-holder alone,
since a message type may not name a node type, a subscription entry must name a
node, and the node-reset route requires a prior failure and performs no
dispatch. Killing the instance released nothing: the instance reports
terminated, the producer received no Release, both rows stay committed and held,
and an instance created afterwards is still refused, so the scope stays occupied
with no live owner.

## Compliance

The body prescribes mechanism: it names the claim-handle row and its states, the
auto-terminal transition, the reaping it is exempted from, and the template
directive that expresses co-holdership. Compliant text: "As a template author
wiring a workflow whose claimed location must outlive a single run, I can mark
that claim as one to keep, work against it from other nodes in the same run and
in later runs of the same instance without taking it again, and rely on nothing
but my own action or the instance ending to give it up, so that a produced
asset stays owned between runs instead of being re-taken or lost."
