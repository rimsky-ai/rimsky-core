---
experiment: event-log-read
commit: PENDING
---

# One feed, four kinds of activity, in clock order

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, no external
services: the template uses only built-in executors. One instance is made to
produce all four kinds of activity the story names — a looping node's lifecycle,
a breakpoint armed on a downstream node, a message the graph sends and a message
the operator posts, and the supervisor's own bookkeeping. The whole run is then
reconstructed from `GET /v1/events`.

## What was observed

Twenty-eight checks, none failing.

The feed carried all four kinds: `work_started` and `terminal/*` for node
lifecycle, `breakpoint.hit` twice for the armed breakpoint, two message
deliveries on the declared message type's own node (the one the graph sent and
the one the operator posted, with the posted payload's value on the record), and
the supervisor's `attributes_substituted` and `work_completed` rows carrying the
supervisor id.

The order was the clock's: the feed came back ordered by its sequence, the
sequence agreed with the timestamps, and the kinds interleaved rather than
grouping — adjacent-pair changes of kind outnumbered the distinct kinds, which a
kind-grouped listing cannot do.

Filtering narrowed and agreed with the whole feed: each of four kinds returned
only that kind and exactly the count the unfiltered feed carried. A mid-run
second as `since=` returned exactly the events at or after it — a real narrowing
of 24 events to 14 — with nothing earlier, and an `until=` set before the run
returned nothing. Malformed filters were refused rather than ignored: an unknown
kind, a non-RFC3339 timestamp, and a malformed instance id each came back 400.
