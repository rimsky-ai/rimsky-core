---
experiment: fanout-list-array
commit: d977250c
---

# A fan-out over a list an upstream node produced

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with the
bundled filesystem claim producer configured over a throwaway workspace, and
drives it through the control API. The template's first node writes a
three-element list as its own attribute; the second node holds a claim on the
bundled producer and names that attribute as its `partition_request`. Nothing in
the run supplies a claim producer of its own: the only producer is the one the
image ships, and the container log confirms it registered.

## What was observed

The upstream node produced the three-item list. The producer split the parent
claim into three sub-scopes, and the fan-out dispatched three work units keyed
`item-1`, `item-2`, `item-3` — one per list element. Each work unit resolved its
own partition key into its attribute bag, and the three keys observed were
exactly the three the list declared. The node's run summary reported four fresh
runs (the parent plus its three work units) and no failures.

Six checks, none failing.
