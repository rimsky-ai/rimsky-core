---
concept: supervisor
status: as-is
aliases: []
---

# Supervisor

## What it is

One of the three rimsky runtime binaries. Implements the acquisition transaction, dispatch, terminal handling, auto-terminal. Registers itself in a persisted supervisor-registry record at startup carrying its configured dispatch concurrency and the callback host and port executors dial back on; service routing is not part of the registration — executor and store names resolve at dispatch time against the shared `concept:service-address-book`. Per-dispatch liveness is observable via a progress timestamp on the persisted node-run row (bumped by scratch writeback, by mid-dispatch attribute writebacks, and by keepalive callbacks) and via the supervisor's outbound peer-connection state for synchronous dispatches.

The callback listener carries three per-run routes alongside the async-callback route: a keepalive route (no body; bumps the progress timestamp), an attribute-writeback route (merges an attributes delta onto the run's live bag and bumps the progress timestamp in the same transaction, accepted only while the run is running or held), and the async-terminal callback. The keepalive and attribute-writeback routes authenticate via the dispatch's cancel token presented as a bearer credential; the contract is no-content on success, unauthorized on a missing or foreign token, not-found on an unknown run, and bad-request on a malformed run id or body (see `decision:keepalive-endpoint`, `decision:writeback-bumps-progress`).

## Purpose

The supervisor is rimsky's worker side. It selects candidate work, performs the atomic acquisition transaction, invokes the executor's execute method, handles terminal events, fires auto-terminal verbs. Multiple supervisors run concurrently and coordinate only through Postgres.

## Boundaries

Owns: the acquisition tx, the dispatch call, terminal-handler resolution, the inbound callback listener, per-dispatch liveness tracking (the progress-timestamp maintained via scratch writeback and keepalive callbacks), the runner's checkpoint wiring that invokes breakpoint evaluation at the pre-dispatch and post-terminal points in the dispatch flow, and the blocked-runner polling loop for resume (see `concept:breakpoint` for the matcher evaluation and hit-recording itself). Does NOT own: scheduling (see `concept:sensor`), control-plane (see `concept:control-api`), claim-state mutation outside the tx (see `concept:claim-producer`), the service name-to-endpoint catalog (see `concept:service-address-book`). Adjacent: `concept:node-run`, `concept:claim-handle`, `concept:executor`, `concept:service-address-book`, `concept:frame`, `concept:error-policy`, `concept:auto-terminal`, `concept:lifecycle-subscriber`, `concept:host-agent-proxy`.

Executor name resolution carries the dispatch's instance / run-scope identity, so a resolver can do instance-aware lookups (the late-bind resolver consults the instance's service bindings; the address-book resolver ignores the added context). The supervisor process also dials outbound `concept:lifecycle-subscriber` peers via the same protocol-membership walk control-api uses, maintains its own subscriber registry, and fires the run-scope-terminal event synchronously after it closes a run scope. Outbound peer dials attach the originally-requested service name to each call so a `concept:host-agent-proxy` fronting a protocol can route the call by name.

## Invariants

- All claim-handle mutations and claim releases by this supervisor are guarded by a predicate matching the acting supervisor's own id, so a supervisor can only mutate handles it holds (invariant 4).
- Verify-before-run: after the acquisition tx commits, re-read the claim's owner and bail as `orphaned_claim_lost_race` if ownership moved (invariant 5).
- Acquisition transaction is rimsky-side atomic: it inserts the claim-handle row (and, for fan-out, all sub-claim handle rows) and records all producer-returned addresses, or none of these (invariant 10).
- The open verb fires inside the rimsky-side acquisition transaction (invariant 15).
- Candidate selection does not filter on service names: any supervisor may claim any node-run. The run's executor name and required store names resolve after acquisition against the shared `concept:service-address-book`; a name absent there resolves late-bound from the instance's service-bindings catalog (see `concept:host-agent-proxy`); a name that resolves nowhere fails the dispatch with an unresolved-service error.
- Two distinct callback hostnames: the listener binds on the all-interfaces address; executors dial back via a separately configured advertised host. When the advertise host is unset and the listener binds a wildcard address, the supervisor refuses to start rather than stamping an unreachable wildcard callback URL into dispatches; an explicit non-wildcard bind host remains a legal advertise fallback.
- Every superseding dispatch is stamped with its predecessor identity and disposition: the quiet-period reap stamps stale-recovery on the released row, an error-policy-resolved retry stamps retry-after-error on the in-place-retried row, and operator recalculate stamps recalculate on the new row. The stamp is persisted on the node-run row, so a restarted supervisor re-emits it on the next dispatch (see `concept:node-run`).
- Candidate selection skips paused instances. A pause-mode breakpoint with an unresumed hit does not affect candidate selection; it blocks the runner after acquisition, at the checkpoint that recorded the hit.
- An async-terminal callback is honored only while its run is running or held, checked atomically with the state mutation it drives; a callback arriving after the run has settled applies nothing and is acknowledged with an authoritative ack-status naming the rejection rather than an error, so a late or duplicate delivery has nothing to retry against.
