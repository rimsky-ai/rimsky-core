---
audit: resume-preserves-snapshot
artifact: story:resume-preserves-snapshot
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T09:45:00Z
---

# A parked run resumes on its own inputs only when nothing upstream moved

Unsupported: the story's load-bearing clause — the resumed executor sees what it
parked with even if upstream nodes re-ran during the park — is contradicted on a
running deployment driven through the control API. Where nothing upstream moves
the promise holds: a worker parked against a rate-limited endpoint, woke on its
own retry schedule, and the endpoint received a second request carrying the same
body from the same run, which settled once. Where an upstream re-runs during the
park — driven through the only public path the product allows, the debug-override
channel, which refuses an instance that is neither paused nor at a breakpoint —
the parked run is not resumed at all. A wake is recorded against the worker, but
the work that then runs is a different run whose inputs were re-substituted from
the freshened upstream: the endpoint received the newer value, the parked run
never settled, and the endpoint saw exactly two requests across the whole
episode, so the parked unit of work was never re-executed. Thirteen checks across
the two ways, three failing, all three in the upstream-moved way, and the same
three failed on two independent runs. The same behaviour is what
`story:cascade-defers-during-flight` observes from its own side.
