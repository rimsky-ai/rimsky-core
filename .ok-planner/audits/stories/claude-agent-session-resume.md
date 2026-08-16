---
audit: claude-agent-session-resume
artifact: story:claude-agent-session-resume
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:58:36Z
---

# An agent conversation continues across re-fires in one run-scope and starts fresh in a child

Supported. Driven through the public surface against a released-image stack
running the bundled agent executor with a stand-in agent binary that keys its
conversation state by the session it is told to resume, so continuity is
observable rather than asserted. Nine checks, none failing. The main graph's agent
node re-fired three times inside one frame on its own success signal: the first
dispatch was spawned fresh, each later one resumed the immediately prior
dispatch's session, all three reported the name the first turn had established,
and all three carried the same run-scope. The delegated child graph's agent node
ran once in a run-scope of its own, was spawned without resuming the parent's
session, and recalled its own conversation rather than the parent's — the reset
across the sub-graph boundary the story asks for.
