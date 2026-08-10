---
experiment: host-agent-late-bind-all-protocols
commit: PENDING
---

# One late-bound binding, two protocols: the executor half works, the claim half does not

## What it ran against

A `rimsky-all-in-one` stack and a `rimsky-host-agent-proxy`, both from the
tree's own image tag, on one docker network, with a `rimsky` CLI host-agent on
the host. The deployment's configuration names the proxy twice — once as an
executor entry and once as a claim-producer entry — and maps both late-bind
protocol slots (`executor` and `claim_producer`) to it. `peer/` is the local
binary: its own Go module, depending only on rimsky's protocols module, serving
the executor and claim-producer protocols and reporting its pid, arguments,
working directory and PEER-prefixed environment.

Four instances run against the same binding: one node whose executor is the
late-bound service, one node whose claim names the late-bound service, one node
whose claim names the deployment's configured proxy entry directly, and one
whose binding path does not exist.

## What was observed

The executor half works end to end. The node settled fresh, the attributes on
the record carry the local binary's own report — its label, its pid, its
working directory — and the binary's log shows one Execute call for the node.
The agent spawned exactly one child from the declared path.

The claim-producer half does not. The node whose claim names the late-bound
service settled failed with `acquire/unresolved_claim_producer`, naming the
late-bound service as the producer it could not resolve. The local binary was
never spawned for it and served neither Open nor Commit.

The control separates an unresolvable name from an unreachable proxy. A node
whose claim names the deployment's configured proxy entry directly reached the
proxy and came back with the proxy's own `binding_not_found`, so the
claim-producer entry the deployment holds for the proxy is live and answering.
The late-bound name is what the deployment fails to resolve to it, not the
proxy.

The fourth instance behaved as the story expects a bad binding to: it settled
failed carrying the agent's own `spawn_failed`.

RESULT: FAIL — the claim-producer protocol is not reachable through a
late-bound binding in this deployment, while the executor protocol is.
