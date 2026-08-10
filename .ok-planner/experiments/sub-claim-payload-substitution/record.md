---
experiment: sub-claim-payload-substitution
commit: PENDING
---

# The standard claim payload directive in a fan-out child's context

## What it ran against

`run.py` boots a `rimsky-all-in-one` container from this tree's image with the
bundled filesystem claim producer configured over a bind-mounted workspace. The
producer's config declares two pick policies, each of which supplies a payload
per claimed item. The template declares two nodes carrying byte-identical
attribute sources — `{{claim.q.payload.folder}}` and `{{claim.q.payload}}` — and
differing only in how the claim arrives: one node opens a claim on a pick-policy
selector directly, the other fans out over a `batch_pick` partition request that
pops three items. Both nodes run the built-in attribute passthrough, so the
resolved attributes come back on each run's terminal event.

## What was observed

On the regular Open'd claim the field path resolved to the producer's payload
for that claim and the bare path resolved to the whole payload object. In the
fan-out, three sub-claims were opened and each clone settled carrying the
payload of its own sub-claim: the set of resolved folder values equalled the set
of sub-claim partition keys, no two clones resolved the same value, and the
bare path resolved to the same object whose field the field path had returned.
The resolved attribute shape was identical in both contexts. The parent and all
three clones settled fresh.

Seven checks, none failing.
