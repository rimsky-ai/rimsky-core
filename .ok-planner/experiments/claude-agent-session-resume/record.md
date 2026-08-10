---
experiment: claude-agent-session-resume
commit: PENDING
---

# An agent conversation continuing within one run-scope and restarting in a child

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG`, which
registers the bundled claude-agent executor in-process once
`CLAUDE_CODE_OAUTH_TOKEN` is set, and points
`RIMSKY_EXECUTOR_CLAUDE_BINARY` at a stand-in agent binary the run compiles from
`probe-agent.go.txt` and mounts into the container. A writable directory is
mounted for the stand-in's conversation state. The stand-in keys that state by
the session id it is told to resume, so a dispatch spawned with `--resume` can
recall what the prior turn established, and a dispatch spawned without it
cannot.

The template declares a main graph and a child graph. The main graph's agent
node subscribes to its own `terminal/success` while its reported turn is below
three, so it re-fires inside one frame. A caller node delegates to the child
graph, whose entry is a passthrough node and whose exit is a second agent node
configured the same way as the first.

## What was observed

The main-graph agent node ran three times inside one frame, reporting turns 1,
2 and 3. The first dispatch was spawned fresh; each later dispatch was spawned
resuming the immediately prior dispatch's session id, and all three reported the
name the first turn established. All three dispatches carried the same
`run_scope_id`.

The child graph's agent node ran once, in a `run_scope_id` different from the
parent's. It was spawned fresh rather than resuming the parent's session, and
its conversation carried its own memory rather than the parent's.

Nine checks, none failing.
