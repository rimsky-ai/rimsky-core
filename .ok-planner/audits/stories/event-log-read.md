---
audit: event-log-read
artifact: story:event-log-read
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# One instance feed carries every kind, in clock order, and narrows on demand

Supported. One instance was made to produce all four kinds of activity the story
names and the run was reconstructed from the single event feed: node lifecycle
transitions, two breakpoint hits, message activity in both directions with the
posted payload on the record, and the supervisor's own rows carrying its id. The
order is the clock's — the feed came back in sequence order, the sequence agreed
with the timestamps, and the kinds interleaved rather than grouping, with more
adjacent kind-changes than there are distinct kinds. Filtering narrowed and
agreed with the whole: four kinds each returned only themselves and exactly the
count the unfiltered feed carried, and a mid-run second as a lower bound cut
twenty-four events to the fourteen at or after it with nothing earlier, while an
upper bound set before the run returned nothing. Malformed filters were refused
rather than ignored: an unknown kind, a non-RFC3339 timestamp and a malformed
instance id each came back 400.
