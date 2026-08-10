---
experiment: host-agent-per-binding-overrides
commit: PENDING
---

# Environment, arguments, working directory and spawn timeout, declared per binding

## What it ran against

A `rimsky-all-in-one` stack and a `rimsky-host-agent-proxy`, both from the
tree's own image tag, on one docker network, with a `rimsky` CLI host-agent on
the host. One template declares two late-bound services and one node each. The
instance binds both to the same binary — the local service built for
host-agent-late-bind-all-protocols — under different environment, arguments and
working directory. A second template and two more instances bind the same
binary with a startup delay of twenty seconds and differ only in the timeout
the binding declares.

## What was observed

Both nodes settled fresh, and each spawned child reported back exactly its own
binding's configuration. The first child reported the environment variable
value `vanilla` and the label `alpha-binding`; the second reported `chocolate`
and `beta-binding`. The first received the four-element argument vector its
binding declared and the second the two-element one. Each reported the working
directory its binding named, and the two ran as separate processes, so neither
binding's configuration leaked into the other.

The timeout is honoured in both directions. With a two-second timeout declared,
the twenty-second binary failed to spawn and the node settled failed carrying
the agent's `spawn_failed`. With a sixty-second timeout declared and nothing
else changed, the same binary spawned, served the dispatch, and the node settled
fresh.

RESULT: PASS
