---
experiment: anonymous-agents-isolated
commit: PENDING
---

# Two anonymous agents, no displacement and no cross-talk

## What it ran against

An anonymous-mode `rimsky-all-in-one` stack and a `rimsky-host-agent-proxy`,
both from the tree's own image tag, on one docker network. Two host agents are
started on the machine with `rimsky agent start`, each with its own state
directory, its own identity file and its own routing label
(`sparkling-wombat`, `grumpy-otter`), and each carrying a different `PEER_LABEL`
in its environment so the binary it spawns identifies itself. The late-bound
service is the third-party peer built for permissive-peer-build. The agent hands
each child a loopback enrolment endpoint of its own, so the agents are started
with `RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT=1` for that hop.

## What was observed

The deployment reported anonymous mode with no keys minted. Both agents
connected to the same proxy and both stayed connected — the second registration
did not displace the first, and `rimsky agent status` reported both connected
before and after the work.

Two instances were created, each naming one agent as its target; the control API
stamped each instance with that agent's routing identity. Both dispatches
settled fresh, and each carried the writeback of the binary its own agent
spawned: `alpha-peer` on developer A's node, `beta-peer` on developer B's. Each
agent's log shows exactly one spawn, one child announcing that agent's peer
label, and exactly one execution — so neither agent saw the other's dispatch.

A third instance targeting an agent nobody is running settled failed with no
writeback, and the execution counts on both agents stayed at one, so an
unroutable dispatch is not absorbed by somebody else's agent.
