---
concept: host-agent-proxy
status: as-is
aliases: []
---

# Host agent proxy

## What it is

A rimsky-stack `concept:service` implementing the multi-protocol composition pattern (per `concept:service` invariants: distinct handler types per protocol, separately registered on one gRPC server). Presents the rimsky gRPC service protocols (`concept:executor`, `concept:claim-producer`) on the supervisor-facing side. Maintains agent connections on the dev-facing side via a new long-lived bidi-stream protocol (the agent-connection protocol). Routes dispatches to whichever agent is connected for the instance's owner. Declared in the rimsky config (`concept:rimsky-yml`) once per protocol it serves — an entry under the executor block, one under the claim-producer block, and so on, all pointing at the same binary.

## Purpose

Lets rimsky dispatch work to dev-machine binaries declared per-instance, without changing any supervisor or graph-processing code path. The proxy is the single architectural addition; supervisors, dispatch resolution, error vocabulary, and callback handling are unchanged.

## Boundaries

Owns: the agent ↔ proxy bidi-stream protocol, the spawn-lifecycle state machine, the per-instance service-bindings cache (populated via `concept:lifecycle-subscriber`), the per-protocol dispatch handlers that proxy through to spawned processes, the callback-URL rewriting that lets spawned processes post to the agent's local listener rather than dialing the supervisor. Does NOT own: the rimsky-side service protocols themselves (those are `concept:executor`, `concept:claim-producer`, etc.), the supervisor's dispatch logic, the per-instance state (that's `concept:instance`), the lifecycle-subscriber wire protocol (that's `concept:lifecycle-subscriber`). Adjacent: `concept:host-agent`, `concept:service`, `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:instance`, `concept:rimsky-yml`.

## Invariants

- Implemented via the existing multi-protocol composition pattern on `concept:service` — distinct handler types, no shared capabilities provider.
- One spawn per (run-scope, binding-name), lazy birth on first dispatch, run-scope-lifetime, reaped on run-scope termination.
- Routing resolves the serving agent by the instance owner's api-key for ordinary instances, OR — for owner-less instances created in `concept:anonymous-mode` — by a well-known anonymous routing identity under which the anonymous-mode agent registers. An owner-less-instance dispatch routes to that anonymous agent rather than hard-failing; anonymous mode and late-bound services are not mutually exclusive.
- All dispatch failures surface as executor-error / claim-producer-unavailable terminals on the supervisor-facing protocol — no new synthetic supervisor-side acquire error classes.
- The proxy is declared in the rimsky config per protocol it serves, using the same binary across all entries (one endpoint, N namespace registrations).
- The proxy is the URL-rewriting boundary for rimsky URLs handed to spawned processes (the callback URL specifically; other rimsky URLs follow the same principle as additional protocols are wired).
- The proxy is a transparent forwarder of every rimsky service protocol it fronts (`concept:executor`, `concept:claim-producer`, `concept:publisher`, `concept:validation`, `concept:data-processing`) by one uniform spawn/forward mechanism, each presenting exactly the fronted service's protocol. No protocol ships as a registered-but-unimplemented stub, and no per-protocol special-casing leaves some protocols unable to reach the spawn/forward path. A service that conforms to its own protocol works behind the proxy by construction — so the proxy adds no separate conformance surface (there is no host-agent / proxy conformance suite).
