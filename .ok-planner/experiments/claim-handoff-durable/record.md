---
experiment: claim-handoff-durable
commit: PENDING
---

# A durable claim across dispatches, and what releases it

## What it ran against

A `rimsky-all-in-one` stack from this tree's image pointed at a claim producer
written for this experiment, which logs every verb it receives and serves that
log over HTTP. One template declares an acquirer that opens a claim with durable
lifetime and a downstream node that co-holds it by alias; both run on the
bundled http-node executor, which posts its resolved attributes to a recorder on
the host. A second template competes for the same scope. Later dispatches are
driven by posting an empty message, which is the public way to wake an
instance's roots. Termination is driven by `rimsky instance kill --force`.

## What was observed

The acquirer's claim handle records lifetime durable and state committed, and
the co-holder in that same dispatch settled fresh reading the claim's address by
alias. Exactly one claim-handle row exists for the scope at that point. A
competing instance asking for the same scope settled
`terminal/error/acquire/unavailable`, so the claim still occupies the scope
after the dispatch that took it.

Two things the story promises did not happen.

A later dispatch of the same instance did not co-hold the same row. Waking the
instance re-dispatched the acquirer, which opened a second claim: the producer's
log reads `Open Commit Open Commit`, and the instance then holds two committed
durable claim-handle rows for the same scope. The later dispatch's co-holder did
read the same address, because the producer resolves the same selector to the
same address, but the row it co-held is a new one. No public path was found that
dispatches the co-holder without also dispatching the acquirer: a message whose
type names a node type is refused at template validation, a subscription entry
must name a node, and the node-reset route requires a prior failed run and
performs no dispatch.

Killing the instance did not release the claim. The instance reports terminated,
the producer received no Release for that scope, both durable rows remain
committed and held, and a competing instance created after the termination is
still refused with `terminal/error/acquire/unavailable`. The scope stays
occupied with no instance alive to own it.
