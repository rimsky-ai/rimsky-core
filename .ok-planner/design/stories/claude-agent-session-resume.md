---
story: claude-agent-session-resume
status: as-is
aliases: []
---

# CLI session continues within one frame's RunScope, fresh in a sub-graph

## Story

As a template author, I can wire claude-agent so its agent CLI conversation continues across multiple dispatches of the agent node that share one frame's RunScope (typically cascade self-edges within a single frame) and starts fresh in a child RunScope (sub-graph invocation), so cascade-driven agent loops preserve reasoning continuity across re-fires within a frame and reset across sub-graph boundaries. Cross-frame session continuity is out of this story's scope: a template that wants an agent's CLI session to span frames must ferry the session token through a message body via the standard message-borne cross-frame coupling path (see `concept:message`).
