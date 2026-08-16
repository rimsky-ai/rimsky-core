---
assessment: event-log-read--filter-by-kind
subject: story:event-log-read
way: filter-by-kind
release: d977250c
outcome: held
warrant: experiment:event-log-read
---
# Narrowing the feed to one kind of activity

Each of the four kinds of activity on the instance was asked for on its own through `catalog:http-routes/GET /v1/events`, and each filtered read agreed with the unfiltered feed: it returned only that kind and exactly the count the whole feed carried for it. Filtering therefore narrows the same record rather than answering from a different one, which is what makes the filtered view usable for reconstructing what happened. An unknown kind was refused outright rather than silently ignored, so a mistyped filter cannot come back as a confidently empty answer. The kinds narrowed this way were node lifecycle, `catalog:event-kinds/breakpoint.hit`, message activity, and the supervisor's own decisions.

## Unverified remainder

None: the passing run demonstrates the way as promised.
