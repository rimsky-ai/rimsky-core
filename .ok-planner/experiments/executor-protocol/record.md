---
experiment: executor-protocol
commit: PENDING
---

# A third-party executor plugs into a stack on the protocol alone

## What it ran against

`peer/` is the permissive-peer-build experiment's third-party executor, extended
until it exercises the whole handshake: a closed expected-attributes schema, two
declared error classes, two declared tags, and the four outcomes Execute can
return. It is still its own Go module whose only rimsky requirement is the
protocols module. `run.sh` cross-builds it, runs it in an alpine container, and
brings up a `rimsky-all-in-one` stack from the tree's own image tag whose only
knowledge of the peer is one `executors:` entry naming its endpoint. Everything
else goes through the control API.

## What was observed

Twenty-two checks, none failing.

At startup the stack's discovery probe reached the peer and reported its whole
advertisement back: both declared error classes, both declared tags, and the
expected-attributes schema.

Rimsky then validated templates against that advertisement. A node declaring
`echo` as an integer was rejected because the executor declares it a string and
"the executor is authoritative on types"; a node declaring a property the peer's
closed schema does not carry was rejected as undeclared; an `error_types` entry
naming a class outside the peer's vocabulary came back as a warning; and a
subscription filtering on a tag the peer never declared was rejected naming the
sender and its executor.

A template written to the advertisement registered, deployed, and ran. The
success outcome settled the node fresh with the peer's attribute delta on the
record. The error outcome settled the node failed. The park outcome parked the
node instead of settling it, and the node carried the `transient/park` signal.
Error routing followed the class the peer raised: the node whose policy mapped
`third-party/refused` to give_up was dispatched exactly once, while the node
whose policy mapped `third-party/broken` to retry was dispatched three times —
once plus its two retries — before failing. Finally the declared tags proved
load-bearing at run time: of two subscribers filtering on different declared
tags of the same sender, the one matching the tag the peer emitted ran and the
other never did.
