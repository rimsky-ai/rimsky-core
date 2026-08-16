---
audit: cascade-defers-during-flight
artifact: story:cascade-defers-during-flight
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T09:45:00Z
---

# An in-flight run is sealed against cascade, but a parked run is replaced rather than woken

Unsupported: the story makes two claims and only the first holds. For a run in
flight, a worker held at a pause-mode pre-dispatch breakpoint while its upstream
was invalidated and settled again kept its own inputs, the running row was
untouched, a second run appeared queued, and that queued run dispatched only
after the first settled and carried the freshened value — one set of inputs in,
one outcome out. For a parked run the promise fails: an outbound HTTP worker
parked against an endpoint answering with an hour-long retry, an upstream cascade
recorded a wake against it within a second, but the work that then ran was a
different run and the parked run never settled. The parked unit of work is
therefore discarded and replaced rather than woken early, and the executor's next
sight of the world is a fresh substitution rather than the continuation the story
promises. Fourteen checks across the two ways, two failing, both in the parked
way. This is the same behaviour `story:resume-preserves-snapshot` observes from
the inputs side.
