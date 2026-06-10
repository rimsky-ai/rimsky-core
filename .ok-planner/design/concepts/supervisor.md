---
concept: supervisor
status: as-is
aliases: []
---

# Supervisor

## What it is

One of the three rimsky runtime binaries. Implements the acquisition transaction, dispatch, terminal handling, auto-terminal. Registers itself in a persisted supervisor-registry record at startup carrying its `accepted_executors` / `accepted_stores` / `concurrency` / `callback_host` / `callback_port`. Heartbeats are queryable timestamps on the persisted node-run rows and claim-handle rows it owns.

## Purpose

The supervisor is rimsky's worker side. It selects candidate work, performs the atomic acquisition transaction, invokes the executor's execute method, handles terminal events, fires auto-terminal verbs. Multiple supervisors run concurrently and coordinate only through Postgres.

## Boundaries

Owns: the acquisition tx, the dispatch call, terminal-handler resolution, callback HTTP server, heartbeating, breakpoint checkpoint evaluation at before_dispatch and after_terminal, blocked-runner polling for resume. Does NOT own: scheduling (see `concept:sensor`), control-plane (see `concept:control-api`), claim-state mutation outside the tx (see `concept:claim-producer`). Adjacent: `concept:node-run`, `concept:claim-handle`, `concept:executor`, `concept:frame`, `concept:error-policy`, `concept:auto-terminal`, `concept:lifecycle-subscriber`, `concept:host-agent-proxy`.

Executor name resolution carries the dispatch's instance / run-scope identity, so a resolver can do instance-aware lookups (the late-bind resolver consults the instance's service bindings; the static resolver ignores the added context). The supervisor process also dials outbound `concept:lifecycle-subscriber` peers (via the same protocol-membership walk control-api uses — no new top-level config block), maintains its own subscriber registry, and fires the run-scope-terminal event synchronously after it closes a run scope. The supervisor's outbound dial config for every peer service installs a client-side interceptor that attaches a per-call service-name header taken from the call context, so a `concept:host-agent-proxy` fronting a protocol can route the call by the originally-requested service name.

## Invariants

- All claim-handle mutations and claim releases by this supervisor are guarded by a predicate matching the acting supervisor's own id, so a supervisor can only mutate handles it holds (`@blessed-invariant 4`).
- Verify-before-run: after the acquisition tx commits, re-read the claim's owner and bail as `orphaned_claim_lost_race` if ownership moved (`@blessed-invariant 5`).
- Acquisition transaction is rimsky-side atomic; the claim-producer open verb runs in its own decoupled tx (`@blessed-invariant 10`).
- The open verb fires inside the rimsky-side acquisition transaction (`@blessed-invariant 15`).
- `accepted_executors` / `accepted_stores` filter candidate selection: a node-run is selectable only when its required-stores set is contained in the supervisor's accepted-stores set. The filter is extended with a late-bind clause: a node-run whose executor (or required store) is NOT in the static accept-lists is still selectable when the configured late-bind proxy name IS in the accept-list AND the instance's service-bindings catalog carries the named binding (see `concept:host-agent-proxy`). With no proxy configured the extension is inert and the original filter applies unchanged.
- Two distinct callback hostnames: the listener binds on the all-interfaces address; executors dial back via a separately configured advertised host.
- Candidate selection skips paused instances and dispatches matching pause-mode breakpoints with unresumed hits.
