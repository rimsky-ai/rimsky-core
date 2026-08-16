---
audit: anonymous-agents-isolated
artifact: story:anonymous-agents-isolated
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:52:00Z
---

# Two developers share an anonymous deployment without displacing or crossing each other

Supported. An anonymous-mode deployment with no keys minted, one agent proxy,
and two host agents started on the machine with separate state directories,
identities and routing labels. Both agents connected to the one proxy and both
stayed connected — the second registration did not displace the first, and both
reported connected before and after the work. Two instances were created, each
naming one agent as its target, and the deployment stamped each instance with
that agent's routing identity. Both dispatches settled fresh, each carrying the
writeback of the binary its own agent spawned, and each agent's log showed
exactly one spawn, one child announcing that agent's own label and exactly one
execution, so neither agent saw the other's dispatch. A third instance aimed at
an agent nobody was running settled failed with no writeback, and both agents'
execution counts stayed at one, so an unroutable dispatch is not absorbed by
somebody else's agent.
