---
experiment: executor-reads-dispatch-context
commit: PENDING
---

# An agent script reading its dispatch identity and disposition

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG`, which
registers the bundled claude-agent executor and the bundled filesystem claim
producer in-process, and points `RIMSKY_EXECUTOR_CLAUDE_BINARY` at a stand-in
agent binary the run compiles from `probe-agent.go.txt` and mounts into the
container. The stand-in calls the executor's `dispatch_context_read` tool and
writes what it read into the node's output attributes.

The template declares four nodes. One agent node just reads and reports. One
agent node blocks without reporting on a dispatch that has no predecessor and
declares `max_quiet_period: 2s`, so the runtime reaps its quiet dispatch and
re-dispatches the same node-run. One fan-out node partitions a filesystem claim
in two. One agent node subscribes to that fan-out node, so the fan-out's
settlement recalculates it.

## What was observed

The plain agent node read a `dispatch_id` equal to the dispatch id rimsky
recorded for that run, a non-empty `run_scope_id`, and a null
`prior_dispatch_id` and `prior_dispatch_disposition`.

The blocking node's second dispatch read a non-null `prior_dispatch_id` and
`prior_dispatch_disposition: stale_recovery`, and reported success on that
branch — the branch its script takes only when a predecessor is present.

The fan-out receiver ran twice: the first dispatch read no predecessor, the
second read `prior_dispatch_disposition: recalculate` naming the first as its
predecessor.

Across the run the script observed three distinct dispositions — none,
`stale_recovery` and `recalculate` — from the dispatch context alone.

Eight checks, none failing.
