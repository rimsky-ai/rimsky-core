---
issue: event-log-domain-for-peer-delivery-health
kind: audit
category: unspecified
artifacts:
  - concept:event-log
  - decision:event-log-kind-enum
  - concept:lifecycle-subscriber
status: verified
opened: 2026-08-21T22:52:41Z
---

# Which operator surface owes a stalled peer delivery?

Two retry loops deliver control-plane events to peers — the lifecycle reconciler (2-second tick) and the producer-verb outbox (widening backoff) — and both report failure only to the structured process log. `concept:event-log` promises the operator "asks the ledger instead of reconstructing history from process output", and its kind vocabulary is a closed enum (`decision:event-log-kind-enum`), so nothing about a stalled peer delivery is filterable there. The producer-verb rows at least have an operator read surface (`route:GET /admin/diagnostics/producer-outbox`); the staged lifecycle rows and the lifecycle ledger have none. A permanently unreachable subscriber is invisible except in log output.

The failure state is also transient where it matters: the reconciler counts consecutive failures in memory, reset on restart, so "first failure" and "recovered" are not persisted transitions today.

The events standard is satisfied at the emit sites (structured kinds with fields); this issue is the product question the standard does not reach — which surface owes the operator this health signal.

## Options

- **Per-attempt event-log kinds.** Every failed attempt writes a ledger row. Foreclosed by volume: a 2-second loop against one dead subscriber writes tens of thousands of rows a day carrying no new information.
- **Stall/recover edge kinds.** Two new enum kinds, written when a peer's delivery first stalls and when it recovers. Bounded volume and filterable in the feed; costs a proto change plus persisting the failure state the reconciler currently keeps in memory.
- **A lifecycle diagnostics route.** Mirror the producer-outbox route for the staged lifecycle rows and ledger. Cheapest and matches the existing shape; the operator polls a route rather than filtering the ledger, leaving the concept's "asks the ledger" promise unmet for this class.
- **Declare peer-delivery health out of event-log scope.** Structured logs, the outbox tables, and the public metrics endpoint carry it; the concept is narrowed to instance execution and auth. Zero code; narrows a live concept's Purpose.

The ruling decides which surface owes the operator a stalled peer delivery, and so what the event log is for.

## Ruling

> Recommended ruling (/verify-issues): give the staged lifecycle rows
> and ledger a diagnostics read route beside the producer-outbox one
> now, and add the stall/recover edge-kind pair to the event log —
> persisting the reconciler's failure state — as the filterable
> signal; do not narrow `concept:event-log`, and reject per-attempt
> rows on the issue's own volume argument.
>
> Rationale: the route alone answers "what is owed right now" but not
> "when did this peer stall", which is exactly the history the
> event-log concept promises the operator, and the edge pair is the
> bounded shape that keeps the promise; the subscription reconciler
> shares the same gap, so the edge kinds should be scoped to cover
> peer delivery generally, not lifecycle alone. Flip case: if the
> owner is narrowing the event log to instance execution and auth
> anyway, take the route-only option and record the narrowing in the
> concept.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
