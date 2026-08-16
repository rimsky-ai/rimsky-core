---
audit: event-log-read
artifact: story:event-log-read
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:45:38Z
---

# One chronological feed carries every kind of instance activity, and filters agree with it

Supported. Driven through the public surface against a container of the released
all-in-one image, on one instance made to produce all four kinds of activity the
story names. Twenty-eight checks, none failing. The feed carried all four:
node lifecycle starts and terminal signals, two breakpoint hits from an armed
breakpoint, two message deliveries — the one the graph sent and the one the
operator posted, with the posted payload's value on the record — and the
supervisor's own bookkeeping rows carrying the supervisor id. Order was the
clock's: the feed came back in sequence order, the sequence agreed with the
timestamps, and the kinds interleaved rather than grouped, which a kind-grouped
listing cannot produce. Filtering agreed with the whole feed on each of the four
kinds, returning only that kind and exactly the count the unfiltered feed
carried; a mid-run bound narrowed twenty-four events to fourteen with nothing
earlier, and a bound set before the run returned nothing. Malformed filters — an
unknown kind, a non-RFC3339 time, a malformed instance id — were each refused
rather than silently ignored.
