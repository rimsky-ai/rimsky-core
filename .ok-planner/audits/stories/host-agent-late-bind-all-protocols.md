---
audit: host-agent-late-bind-all-protocols
artifact: story:host-agent-late-bind-all-protocols
determination: unsupported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
checked: 2
unaccounted: 1
---

# The executor protocol reaches a late-bound binary; the claim-producer protocol does not

Unsupported. Of the two peer protocols a late-bound binding can name, one works
and one does not. Measured against a containerised deployment whose
configuration names its proxy as both an executor entry and a claim-producer
entry and maps both late-bind protocol slots to it, with an agent connected from
the host and one binding declared for a local binary that serves both protocols.

A node whose executor is the late-bound service settled fresh, carrying the
local binary's own report on the record, and the binary served one Execute call
from the child the agent spawned. A node whose claim names the same late-bound
service settled failed with `acquire/unresolved_claim_producer` naming that
service: the deployment never resolved the name, the binary was never spawned
for it, and neither Open nor Commit was served. A control separates an
unresolvable name from an unreachable proxy — a node whose claim names the
deployment's configured proxy entry directly reached the proxy and came back
with the proxy's own `binding_not_found`, so that entry is live and answering.
A binding naming a path that does not exist failed the dispatch with the agent's
own `spawn_failed`, which is the behaviour the story expects of a bad binding.

## Unaccounted

- claim_producer — a node whose claim names a late-bound service is refused with
  `acquire/unresolved_claim_producer` in a deployment where the proxy's
  claim-producer entry is declared in the configuration file, which is the only
  way an operator declares one.
