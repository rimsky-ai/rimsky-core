---
audit: resume-preserves-snapshot
artifact: story:resume-preserves-snapshot
determination: unsupported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:30:00Z
---

# A park keeps its inputs on its own clock, and loses them to an upstream re-run

Unsupported. The story's distinguishing clause is "even if upstream nodes re-ran
during the park", and that is the case a run contradicts. Both of the 2 ways a
park can end were driven against a zero-config all-in-one deployment whose
outbound-HTTP node calls an endpoint that answers 429 once and records every
request body it receives. Where nothing upstream moved, the promise holds: the
node parked having sent its upstream value of 1, the park resumed on its own
retry schedule, the endpoint received a second request with the same body, the
resumed run carried the same run id as the parked one, and the node settled once
across the two dispatches. Where the upstream node re-ran during the park — the
instance paused, the upstream override-invalidated, the instance resumed, which is
the public path the clause describes — the parked run was woken and then replaced:
the work that ran carried a different run id, the endpoint received the freshened
upstream value of 2 rather than the 1 the run parked with, and the parked run
never completed. The endpoint received exactly two requests across the episode, so
the parked unit of work was never re-executed at all. That is the re-evaluation
with rewritten inputs the story says parking must not be, and it reproduced on two
independent runs.
