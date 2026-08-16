---
experiment: host-agent-late-bind-all-protocols
commit: d977250c
---

# One late-bound binding, two protocols: both reach the local binary, by different names

## What it ran against

A `rimsky-all-in-one` stack and a `rimsky-host-agent-proxy`, both from the
tree's own image tag, on one docker network, with a `rimsky` CLI host-agent on
the host. The deployment's configuration names the proxy twice — once as an
executor entry and once as a claim-producer entry — and maps both late-bind
protocol slots (`executor` and `claim_producer`) to it. `peer/` is the local
binary: its own Go module, depending only on rimsky's protocols module, serving
the executor and claim-producer protocols and reporting its pid, arguments,
working directory and PEER-prefixed environment.

Five instances run against the same binding: one node whose executor is the
late-bound service, one node whose claim names the late-bound service, one node
whose claim names the deployment's configured proxy entry with the binding
declared under that same name, one whose claim names that entry with no such
binding, and one whose binding path does not exist.

At this tree the experiment was repaired. It previously concluded that the
claim-producer half does not work at all; the third instance was added and shows
that it does work, under a different declaration.

## What was observed

The executor half works end to end. The node settled fresh, the attributes on
the record carry the local binary's own report — its label, its pid, its working
directory — and the binary's log shows one Execute call for the node. The agent
spawned exactly one child from the declared path.

The claim half works when the node names the deployment's configured proxy entry
and the instance declares a binding under that same name: the node settled
fresh, and the local binary served Open, Execute and Commit against the claim.

The claim half does not work under the name the executor half uses. A node whose
claim names the late-bound service settled failed with
`acquire/unresolved_claim_producer`, naming the late-bound service as the
producer it could not resolve; the local binary was never spawned for it. The
template surface registers and deploys that spelling without complaint, so the
divergence surfaces at dispatch, not at registration.

The control separates an unresolvable name from an unreachable proxy. A node
whose claim names the configured proxy entry with no binding under that name
reached the proxy and came back with the proxy's own `binding_not_found`, so the
claim-producer entry the deployment holds for the proxy is live and answering.

The fifth instance behaved as the story expects a bad binding to: it settled
failed carrying the agent's own `spawn_failed`.

RESULT: FAIL — the two protocols are not declared identically; the claim
producer resolves only under the deployment's proxy-entry name, while the
late-bind name the executor accepts fails the dispatch.
