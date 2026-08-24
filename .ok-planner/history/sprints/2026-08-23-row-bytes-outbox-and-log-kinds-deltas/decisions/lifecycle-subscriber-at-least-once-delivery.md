---
decision: lifecycle-subscriber-at-least-once-delivery
---

# Lifecycle delivery is at-least-once

## Choice

Rimsky delivers each lifecycle event at least once. The transaction that performs a transition stages its event as an outbox row; the drain delivers the oldest pending row per service and per object, retries a failed delivery on a widening interval, and deletes the row in the same transaction that records the service's acknowledgement. Subscriber handlers must be idempotent (see `concept:lifecycle-subscriber`). At-least-once holds unconditionally under the shipped defaults. An operator who sets a positive lifecycle-outbox retention window makes an explicit decision to discard history the service has not acknowledged by that age, and the stall signal (see `decision:service-delivery-stall-signal`) makes the failure visible before the window discards it.

## Rationale

A delivery crosses a process boundary into code rimsky does not control, so rimsky cannot tell a subscriber that never answered from one that did the work and then died. A row staged with the transition survives a crash at any later point, so a subscriber that missed an event eventually receives it. The residual cost is a duplicate after a crash between the service's acknowledgement and the row's deletion, which the subscriber tolerates. Exactly-once would require the subscriber's own side effect and rimsky's row deletion to commit together, which rimsky cannot arrange across an arbitrary service. The retention window follows the shape `decision:message-queue-mode-per-instance` set for bounded-resource trades: a no-loss default and an operator-named discard.

## Alternatives

- Deliver at most once and drop a failed delivery — rejected: a subscriber that is down during a transition never hears of it.
- Pursue exactly-once through a two-phase handshake — rejected: it demands a prepare-and-commit surface from every service, and a service that fails between the phases still leaves rimsky unable to tell whether the side effect happened.
- A separate idempotency ledger beside the outbox — rejected: every job the ledger did — skip an acknowledged delivery, skip a closing event for an object the service never heard open, find the deliveries an instance still owes — the ordered outbox row already does.
- Remove the retention window so at-least-once admits no operator bound — rejected: an abandoned subscriber then grows the outbox without limit, and the stall signal already makes the trade visible.
- Re-derive dropped events from source so the window discards nothing — rejected: the template events and instance creation have no source to re-derive from short of a full scan, which the reconciler work deliberately retired.
