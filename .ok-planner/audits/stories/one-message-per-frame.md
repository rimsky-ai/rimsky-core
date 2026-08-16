---
audit: one-message-per-frame
artifact: story:one-message-per-frame
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:07:07Z
---

# Substitution from the message body stays well-defined under a burst

Supported, and measured where the promise would break if it were going to. Three
distinctly-labelled messages arrived while the instance was held busy on a
breakpoint, so the queue genuinely backed up rather than being delivered one at a
time by luck. Each delivered message named a distinct frame, each frame's
triggering message was one of the three, and the reacting node resolved exactly
one body per run in arrival order, with no run recording a resolution failure.
The same burst was then run against the mode that discards stale wakes — the mode
where merging two bodies into one frame is most plausible — and it too delivered
one message per frame, two across two frames, with one body resolved per run.
Neither mode ever put two bodies in one frame, which is why no template has to
refuse a coalesced frame at runtime. Eight checks, none failing.
