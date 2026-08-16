---
assessment: multi-hard-dep-rendezvous--two-force-refreshed-upstreams
subject: story:multi-hard-dep-rendezvous
way: two-force-refreshed-upstreams
release: d977250c
outcome: held
warrant: experiment:multi-hard-dep-rendezvous
---
# Two force-refreshed upstreams on one node rendezvous instead of livelocking

The audit drove an all-in-one deployment (`catalog:images/rimsky-all-in-one`) through `catalog:http-routes/POST /v1/instances` on a template whose receiver declares three subscriptions: one on a structural-root trigger and two force-refreshed, on upstreams whose only other subscription is to a message type nothing sends. An operator message posted to `catalog:http-routes/POST /v1/instances/{id}/messages` woke the trigger, the receiver's invalidation pulled both upstreams into the frame, each upstream ran exactly once, and the receiver dispatched exactly once with both upstream values present in its outcome. The instance then reached rest with no live runs, so neither upstream re-ran and the shape did not livelock — the receiver computed from the full refreshed set, which is what the story asks for. Four checks, none failing.

## Unverified remainder

The rendezvous was demonstrated at two force-refreshed upstreams on one receiver. The way does not establish the shape at three or more, nor when the upstreams themselves have force-refreshed upstreams.
