---
audit: multi-hard-dep-rendezvous
artifact: story:multi-hard-dep-rendezvous
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:46Z
---

# Two force-refreshed upstreams on one node rendezvous instead of livelocking

Supported: a run through the control API of an all-in-one deployment drove a
receiver declaring three subscriptions — one on a structural-root trigger and
two force-refreshed, on upstreams whose only other subscription is to a message
type nothing sends. The operator message woke the trigger, the receiver's
invalidation pulled both upstreams into the frame, each ran exactly once, and
the receiver dispatched exactly once with both upstream values in its outcome.
The instance then reached rest with no live runs, so neither upstream re-ran and
the shape did not livelock. Four checks, none failing.
