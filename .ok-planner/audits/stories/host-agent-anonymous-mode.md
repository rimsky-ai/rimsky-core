---
audit: host-agent-anonymous-mode
artifact: story:host-agent-anonymous-mode
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# The dev loop runs on a fresh deployment with no credential minted

Supported, measured on a fresh deployment that reported anonymous mode and held
no api-key, with every control-API request in the run sent without an
authorization header and the agent started with no api-key of its own. The agent
connected to the proxy and adopted the routing label the operator asked for.
Registering the template, deploying it, creating the instance and waking it all
succeeded unauthenticated; the deployment stamped the instance with that agent's
routing identity and the dispatch settled fresh with the operator's own local
binary on the record, spawned once. Creating an instance without naming a target
agent was refused with an error saying anonymous mode requires one, and an
instance aimed at a label nobody was running settled failed while the connected
agent's spawn count stayed at one. The deployment was still in anonymous mode
with no api-key when the run finished.
