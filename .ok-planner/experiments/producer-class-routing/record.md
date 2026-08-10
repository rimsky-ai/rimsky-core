---
experiment: producer-class-routing
commit: PENDING
---

# Routing the acquisition error class the producer named

## What it ran against

`run.py` boots a `rimsky-all-in-one` container from this tree's image with the
bundled filesystem claim producer configured over a root path that does not
exist, so every acquisition against it fails with the class that producer
declares in its capabilities handshake, `fs/root_unavailable`. The script then
registers one node template five times through the control API, changing only
the node's `error_types` map, and reads back the node's run summary and the
emitted terminal signal.

## What was observed

The acquisition failure surfaced as `terminal/error/fs/root_unavailable`
carrying `error_class: fs/root_unavailable`, so the producer's own class
reached the routing surface rather than a generic one. A template keying that
class to `pass` settled the node fresh while its `acquire/producer_error` entry
said `give_up`; a template with no producer-class entry and
`acquire/producer_error` keyed to `pass` settled fresh on the same failure, so
the generic acquire-family key is the fallback; a template keying the producer
class to `give_up` and both generic keys to `pass` settled the node failed, so
the producer-class entry outranks the generic key beside it. In every case the
emitted signal carried `fs/root_unavailable`, the most specific class, whatever
key did the routing. Registering the producer's declared class produced no
validation warning, and registering an undeclared `fs/not_a_declared_class`
registered with a warning naming the class and listing the vocabularies checked.

Seven checks, none failing.
