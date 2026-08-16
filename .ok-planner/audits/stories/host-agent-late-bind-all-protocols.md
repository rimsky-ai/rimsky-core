---
audit: host-agent-late-bind-all-protocols
artifact: story:host-agent-late-bind-all-protocols
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:05:00Z
---

# Both protocols reach a spawned local child, but not identically

Unsupported, on the story's claim that the two protocols behave identically. A
deployment in containers mapped both late-bind protocol slots to one agent
proxy, an agent ran on the machine holding the author's binary, and four
instances drove the same binding. The executor half works as the story
describes: a node whose executor is the late-bound service settled fresh, the
attributes on the record carry the local binary's own report, and the agent
spawned exactly one child from the declared path. The claim half does reach a
spawned local child, but only under a different spelling: a node whose claim
names the deployment's own proxy entry, with the binding declared under that
same name, settled fresh and the local binary served Open, Execute and Commit.
Naming the late-bind service — the spelling the executor half accepts, which the
template surface registers and deploys without complaint — settled failed with
an unresolved-claim-producer error and never spawned the binary. A control run
separates the two: naming the configured proxy entry with no binding under that
name reaches the proxy and comes back with the proxy's own binding-not-found, so
the proxy is live and answering, and the fourth instance, whose binding names a
path that does not exist, settled failed carrying the agent's own spawn error.
So the benefit is obtainable for both protocols, but the author has to discover
that the two are declared differently, and the symmetric declaration fails at
dispatch rather than at registration.
