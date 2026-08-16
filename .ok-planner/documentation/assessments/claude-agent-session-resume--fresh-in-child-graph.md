---
assessment: claude-agent-session-resume--fresh-in-child-graph
subject: story:claude-agent-session-resume
way: fresh-in-child-graph
release: d977250c
outcome: held
warrant: experiment:claude-agent-session-resume
---
# A delegated sub-graph's agent starts from a clean conversation

The delegated child graph's agent node ran once in a run-scope of its own. It was spawned without resuming the parent's session, and it recalled its own conversation rather than the parent's. Delegation therefore resets the reasoning context at the sub-graph boundary, which is what an author wants when a child graph is a separate job rather than a continuation of the caller's turn.

## Unverified remainder

None: the passing run demonstrates the way as promised.
