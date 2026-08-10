---
experiment: host-agent-anonymous-mode
commit: PENDING
---

# Registering and dispatching to a late-bound service with no credentials minted

## What it ran against

A `rimsky-all-in-one` stack and a `rimsky-host-agent-proxy`, both from the
tree's own image tag, on one docker network, with a `rimsky` CLI host-agent on
the host. Every control-API request in the run is sent without an
`Authorization` header, the agent is started with no `--api-key` and with
`RIMSKY_API_KEY` removed from its environment, and no key is minted at any
point. The late-bound binary is the local service built for
host-agent-late-bind-all-protocols.

## What was observed

The fresh deployment reported anonymous mode and held no api-key. The agent
connected to the proxy carrying no credential and adopted the routing label the
operator asked for, with the key count still zero.

Registering the template, deploying it, creating the instance and waking it all
succeeded unauthenticated. Creating an instance without naming a target agent
was refused with an error saying a target agent is required in anonymous mode.
Naming the running agent's label produced an instance the deployment stamped
with that agent's routing identity, and the dispatch settled fresh with the
operator's own binary on the record — the agent spawned it once.

An instance aimed at a label nobody was running settled failed, and the
connected agent's spawn count stayed at one, so an unroutable dispatch is not
absorbed by the agent that happens to be connected.

The deployment was still in anonymous mode with no api-key when the run
finished.

RESULT: PASS
