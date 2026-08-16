---
assessment: host-agent-anonymous-mode--anonymous-late-bind
subject: story:host-agent-anonymous-mode
way: anonymous-late-bind
release: d977250c
outcome: held
warrant: experiment:host-agent-anonymous-mode
---
# Running the late-bind dev loop before any credential exists

A fresh deployment reporting anonymous mode and holding no api-key was driven end to end with no authorization header on any request, alongside a `catalog:images/rimsky-host-agent-proxy` and a host agent started with no `catalog:cli-flags/--api-key` and with `catalog:env-vars/RIMSKY_API_KEY` removed from its environment. The agent connected carrying no credential and adopted the routing label the operator asked for, with the key count still zero. Registering the template, deploying it, creating the instance and waking it all succeeded unauthenticated, and the dispatch settled fresh on the operator's own local binary, which the agent spawned once. Creating an instance without naming a target agent was refused with an error saying a target agent is required in anonymous mode, and an instance aimed at a label nobody was running settled failed while the connected agent's spawn count stayed at one — so an unroutable dispatch is not absorbed by whichever agent happens to be connected. The deployment was still anonymous with no api-key when the run finished, so nothing about the loop silently mints a credential.

## Unverified remainder

None: the passing run demonstrates the way as promised.
