---
audit: claude-agent-session-resume
artifact: story:claude-agent-session-resume
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# An agent conversation continues within a run-scope and restarts in a child one

Supported. A template whose agent node subscribes to its own success re-fired
three times inside one frame against an all-in-one deployment carrying the
bundled claude-agent executor. The first dispatch spawned a fresh conversation;
each later dispatch resumed the immediately preceding dispatch's session, and
all three recalled what the first turn established, so the continuity is real
rather than a re-run. All three dispatches carried one run-scope id. The same
template's caller node delegates to a child graph whose agent node is configured
identically: that node ran in a different run-scope id, was spawned fresh rather
than resuming the parent's session, and carried its own memory instead of the
parent's.
