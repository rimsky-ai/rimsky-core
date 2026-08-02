---
story: claude-agent-session-resume
---

# CLI session continues within one frame's RunScope, fresh in a sub-graph

## Story

As a template author, I can wire the bundled claude-agent so its agent conversation continues across dispatches of the agent node that share one frame's run-scope and starts fresh in a child run-scope, so that cascade-driven agent loops preserve reasoning continuity across re-fires within a frame and reset across sub-graph boundaries.
