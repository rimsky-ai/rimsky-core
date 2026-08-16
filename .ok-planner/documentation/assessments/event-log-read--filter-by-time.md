---
assessment: event-log-read--filter-by-time
subject: story:event-log-read
way: filter-by-time
release: d977250c
outcome: held
warrant: experiment:event-log-read
---
# Narrowing the feed to a window of time

A mid-run bound supplied to `catalog:http-routes/GET /v1/events` as the feed's lower bound narrowed twenty-four events to fourteen, returning exactly the events at or after it and nothing earlier, so the bound is a real narrowing rather than a truncation. An upper bound set before the run began returned nothing at all, which is the answer a genuine time filter owes. A timestamp that is not RFC3339 was refused rather than ignored, so an operator cannot get an unbounded feed back while believing it was bounded. Time and kind filters agree with the same unified feed, so an operator can narrow to a window and still trust the counts.

## Unverified remainder

None: the passing run demonstrates the way as promised.
