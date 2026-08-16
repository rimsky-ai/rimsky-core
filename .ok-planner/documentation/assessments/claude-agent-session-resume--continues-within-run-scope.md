---
assessment: claude-agent-session-resume--continues-within-run-scope
subject: story:claude-agent-session-resume
way: continues-within-run-scope
release: d977250c
outcome: held
warrant: experiment:claude-agent-session-resume
---
# An agent conversation carries across re-fires inside one frame

The audit drove a deployment of `catalog:images/rimsky-all-in-one` running the bundled agent executor against a stand-in agent that keys its conversation state by the session it is told to resume, so continuity is observable rather than asserted. Nine checks ran and none failed. The main graph's agent node re-fired three times inside one frame on its own success signal: the first dispatch was spawned fresh, each later one resumed the immediately prior dispatch's session, all three reported the name the first turn had established, and all three carried the same run-scope. A cascade-driven agent loop therefore keeps its reasoning across re-fires instead of restarting from nothing each time the node wakes.

## Unverified remainder

None: the passing run demonstrates the way as promised.
