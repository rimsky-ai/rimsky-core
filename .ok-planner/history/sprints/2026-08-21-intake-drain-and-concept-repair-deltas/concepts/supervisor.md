---
concept: supervisor
---

# Supervisor

## What it is

The supervisor is one of rimsky's three runtime roles: the one that does the work. It runs the acquisition transaction that claims a node-run, dispatches that run to its executor, handles the terminal that comes back, and fires the auto-terminal verbs. At startup it registers itself in a persisted supervisor record carrying its configured dispatch concurrency and the callback host and port executors dial back on. Service routing stays out of that record: a run's executor and store names resolve at dispatch time against the shared `concept:service-address-book`. Every dispatch carries a progress timestamp on its persisted node-run row — bumped by scratch writeback, by mid-dispatch attribute writebacks, and by keepalive callbacks — so an observer can tell a live dispatch from a stalled one; for a synchronous dispatch the supervisor's outbound peer-connection state is a second liveness signal.

## Purpose

The supervisor separates doing the work from deciding what work exists, and it scales that half independently. Many supervisors run at once and coordinate only through the shared persistent store, so a deployment adds dispatch capacity by adding supervisors and needs no coordinator among them. Because candidate work carries no affinity to a particular supervisor, any supervisor may take any node-run and resolve the services that run needs afterwards.

## Boundaries

The supervisor owns the acquisition transaction, the dispatch call, terminal-handler resolution, its inbound callback listener, and per-dispatch liveness tracking. It owns the runner's checkpoint wiring, which invokes breakpoint evaluation before dispatch and after the terminal, and the blocked-runner polling loop that waits for a resume; evaluating the matcher and recording a hit belong to `concept:breakpoint`. It does not own scheduling (see also `concept:sensor`), the control plane (see also `concept:control-api`), claim-state mutation outside the acquisition transaction (see also `concept:claim-producer`), or the catalog that maps a service name to an endpoint (see also `concept:service-address-book`).

Executor name resolution carries the dispatch's instance and run-scope identity, so a resolver can answer per instance: the late-bind resolver consults the instance's service bindings, and the address-book resolver ignores the extra context. The supervisor also dials outbound `concept:lifecycle-subscriber` peers through the same protocol-membership walk the control API uses, keeps its own subscriber registry, and fires the run-scope-terminal event once it closes a run scope. Every outbound peer dial carries the originally requested service name, so a `concept:host-agent-proxy` fronting a protocol can route the call by that name.

See also `concept:node-run`, `concept:claim-handle`, `concept:executor`, `concept:frame`, `concept:error-policy`, `concept:auto-terminal`.
