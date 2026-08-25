---
decision: dispatch-defaults-cover-every-node-timing-key
---

# Every per-node timing key takes a deployment-wide default

## Choice

`max_retries` and `retry_backoff` take their deployment-wide default from the dispatch defaults, beside the three deadlines that `decision:three-dispatch-deadlines` governs. `retry_backoff` defaults as one object: a node that sets `retry_backoff` replaces the whole default object, and a node that omits it takes the whole default object.

## Rationale

An operator sets deployment-wide policy once. A timing key with a deployment default and a timing key without one are two idioms for one job, so every per-node timing key takes a default from the same place. `retry_backoff` defaults whole because a backoff is one policy: kind, base delay, cap, jitter. Merging a node's partial object into the default would let a node change the kind and inherit a base delay chosen for a different kind.

## Alternatives

- Deployment defaults for the three deadlines only, with `max_retries` and `retry_backoff` set per node — rejected: an operator who wants one retry policy repeats it on every node.
- Per-subkey merging of `retry_backoff` — rejected: a node's partial object would inherit values chosen for a different backoff kind.
