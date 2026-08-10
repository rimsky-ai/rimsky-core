---
audit: one-message-per-frame
artifact: story:one-message-per-frame
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# A burst of three messages arrives at a busy instance and never shares a frame

Supported. Against a zero-config all-in-one deployment, a pause-mode breakpoint
held the first frame open while 3 distinctly-labelled messages of one declared
type arrived. The backlog instance then delivered all 3 across 3 frames, each
delivered message naming a distinct frame and each frame's triggering message
being one of the 3; its reacting node settled 3 times, resolving exactly one
label per run in arrival order, with no template-resolution failure on any run.
The same burst against a coalescing instance delivered 2 messages across 2
frames, again one body per frame and one label per run. Neither of the 2 queue
modes put two bodies in one frame, so no run of the template had a
multi-message frame to refuse.
