---
decision: lifecycle-subscriber-at-least-once-delivery
---

# Lifecycle delivery is at-least-once

## Choice

Rimsky delivers each lifecycle event at least once. A persisted idempotency ledger, keyed by service, event type, and object, absorbs a replay of a delivery it already recorded. Subscriber handlers must be idempotent (see `concept:lifecycle-subscriber`).

## Rationale

A delivery crosses a process boundary into code rimsky does not control, so rimsky cannot tell a subscriber that never answered from one that did the work and then died. Retrying until the ledger records success means a subscriber that missed an event eventually receives it. The residual cost is a duplicate the subscriber tolerates. Exactly-once would require the subscriber's own side effect and rimsky's ledger write to commit together, which rimsky cannot arrange across an arbitrary peer.

## Alternatives

- Deliver at most once and drop a failed delivery — rejected: a subscriber that provisions substrate at template deploy silently never provisions it.
- Pursue exactly-once through a two-phase handshake — rejected: it demands a prepare-and-commit surface from every peer, and a peer that fails between the phases still leaves rimsky unable to tell whether the side effect happened.
