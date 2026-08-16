---
audit: host-agent-anonymous-mode
artifact: story:host-agent-anonymous-mode
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:05:00Z
---

# The late-bind dev loop works on a deployment with no credentials minted

Supported. A fresh deployment reporting anonymous mode with zero api-keys was
driven end to end with no authorization header on any request and an agent
started with no key in its environment. The agent connected carrying no
credential and adopted the routing label the operator asked for, with the key
count still zero. Registering the template, deploying it, creating the instance
and waking it all succeeded unauthenticated; creating an instance without naming
a target agent was refused with an error saying a target agent is required in
anonymous mode; naming the running agent's label produced an instance the
deployment stamped with that agent's routing identity, and the dispatch settled
fresh on the operator's own local binary, which the agent spawned once. An
instance aimed at a label nobody was running settled failed while the connected
agent's spawn count stayed at one, so an unroutable dispatch is not absorbed by
whichever agent happens to be connected. The deployment was still anonymous with
no api-key when the run finished.
