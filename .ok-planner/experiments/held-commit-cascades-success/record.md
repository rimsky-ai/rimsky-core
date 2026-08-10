---
experiment: held-commit-cascades-success
commit: PENDING
---

# The success of held work reaches a downstream subscriber only at commit

## What it ran against

`run.py` starts a gate endpoint on the host that holds every request open until
the script posts to its release route, then boots a `rimsky-all-in-one`
container from this tree's image with the bundled filesystem claim producer
configured over a bind-mounted workspace. The template declares three nodes: an
acquirer that opens a claim on that producer, a co-holder that holds the same
claim and calls the gate endpoint, and a watcher that is not a member of the
holding subgraph and subscribes to the acquirer's success. The gate endpoint
reports when the co-holder's request arrives, which is the script's
synchronisation point rather than any wall-clock wait.

## What was observed

When the co-holder's request reached the gate endpoint, the acquirer's node-run
was in state `held`, the acquirer had emitted no success signal, and the watcher
had no node-run at all. After the script released the gate, the claim resolved
with a single `claim_resolution.commit`, the acquirer emitted exactly one
`terminal/success` at the next sequence number after that commit, and the
watcher's `work_started` followed the commit. The downstream subscriber
therefore observed the success at commit and not at the provisional held moment.

Seven checks, none failing.
