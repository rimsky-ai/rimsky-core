---
audit: breakpoint-debugger
artifact: story:breakpoint-debugger
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# Install, observe, resume with an overlay, and delete a breakpoint

Supported. Against a zero-config all-in-one deployment, all 4 acts the story
names were driven in one session on one live instance. A breakpoint installed on
the worker's before-dispatch checkpoint and read back off the instance; the
worker's first dispatch stopped there and appeared both on the hits ledger —
naming the worker's node, its sealed dispatch bag, and the run it was holding in
flight — and on the unified event log as one hit record naming the same
breakpoint. Resuming that hit with an attribute overlay re-fired the dispatch
carrying the overlaid value and leaving every value the overlay did not name
alone. Deleting the breakpoint removed it from the instance and emptied its hits,
while the event log kept its record of the hit.

## Compliance

The body prescribes mechanism in two places: it names the two stores a hit lands
in, and it names an internal role as the actor that applies the overlay ("that
the supervisor applies on re-fire"). A story states what the user observes, not
where the system keeps it or which part produces it. Compliant text: "As an
operator, I can install a breakpoint on a running instance, see each hit it takes,
release a held hit with an attribute overlay that the re-fired work then runs
with, and delete the breakpoint to clear its hits, so that I debug a live
instance."
