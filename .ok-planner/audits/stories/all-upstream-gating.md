---
audit: all-upstream-gating
artifact: story:all-upstream-gating
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# A fan-in receiver never runs against a half-settled upstream set

Supported. A template declaring a receiver over two upstreams was driven through
the control API with one upstream held in flight at a pause-mode breakpoint. The
other upstream settled twice while it was held — once with staleness arriving by
cascade and once by operator invalidation — and the receiver did not dispatch on
either. Releasing the held upstream let the receiver dispatch, and no receiver
dispatch preceded the last upstream settlement in the frame's event log. The
receiver's outcome carried both upstream values, so it computed from the settled
set rather than from a partial one. Two of the three staleness routes a
receiver's upstream can take were exercised; the third, message delivery, woke
the held upstream itself.
