---
experiment: claim-handoff-durable
commit: d977250c
---

# A durable claim across dispatches, and what releases it

## What it ran against

A `rimsky-all-in-one` stack from this tree's image pointed at a claim producer
written for this experiment, which logs every verb it receives and serves that
log over HTTP. One template declares an acquirer that opens a claim with
durable lifetime and a co-holder that shares it by alias. The co-holder is
woken two ways: by the acquirer settling, and by a declared message type, so a
later message dispatches the co-holder alone in a frame of its own without
re-dispatching the acquirer. The co-holder runs on the bundled http-node
executor, which posts its resolved attributes to a recorder on the host. A
second template competes for the same scope. Later dispatches are driven by
posting a message of the declared type, termination by
`rimsky instance kill --force`, and deletion by `rimsky instance delete`.

A co-holder that can only be woken by a later message and never by the
acquirer does not work: every node the template declares as a co-holder is a
member of the acquirer's holding subgraph, so the acquirer's claim never
reaches auto-terminal while one of them has not run, and the frame that would
have to settle before the waking message is delivered never settles. The
co-holder must run in the acquirer's own dispatch as well as in the later one.

## What was observed

The acquirer's claim handle records lifetime durable and state committed after
its dispatch, and the co-holder in that same dispatch settled fresh reading the
claim's address by alias. Exactly one claim-handle row exists for the scope. A
competing instance asking for the same scope settled
`terminal/error/acquire/unavailable`, so the claim still occupies the scope
after the dispatch that took it.

A message of the declared type then woke the co-holder into a second dispatch
of its own. It settled fresh, read the same address by alias, and registered
as a third holder on the first dispatch's claim-handle row: the row count for
the scope stayed at one, the row id was unchanged, and the producer's log for
the scope still reads `Open Commit` — one Open across both dispatches. Nothing
in either dispatch asked the producer to release.

Terminating the instance released nothing: the producer received no Release,
the committed durable row remained, and a competing instance created after the
termination was still refused. Deleting the terminated instance is the operator
action that releases: the producer's log gains `Release`, the row is gone, and
a competing instance created afterwards claims the scope and settles fresh.
