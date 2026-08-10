---
audit: anonymous-agents-isolated
artifact: story:anonymous-agents-isolated
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# Two anonymous agents on one deployment stay each other's business

Supported. Two host agents were started on one machine against one proxy on an
anonymous-mode deployment, each with its own routing label and its own spawned
service binary, and both stayed connected — the second registration displaced
nothing. Each developer's instance was stamped with its own agent's routing
identity, both dispatches settled successfully, and each carried the writeback of
the binary its own agent spawned; each agent's log records exactly one spawn and
exactly one execution, so neither saw the other's work. A third instance aimed at
an agent nobody was running settled failed with no writeback, and both agents'
execution counts stayed at one, so an unroutable dispatch is not absorbed by
somebody else's agent.
