---
concept: publisher-subscription
status: as-is
aliases: [sensor-watch]
---

# Publisher-subscription

## Definition

A publisher-subscription is the rimsky↔publisher binding state for one (instance, publisher, type) triple. Created at instance creation when the template declares a publisher entry; lives in a persisted publisher-subscription ledger; carries an opaque subscription identifier; dropped at instance termination.

A publisher-subscription is the rimsky-side mirror of the publisher's per-binary state. The publisher holds the substrate-specific state (cron schedule, body hash, watermark cursor); rimsky holds the binding metadata (which publisher, which instance, which message type).

## Naming note

Named `publisher-subscription` rather than `subscription` because `concept:node-subscription` already owns the receiver-side template-DSL `subscribes:` block. The two are orthogonal: a publisher-subscription is a publisher↔rimsky binding; a node-subscription is one template node's wait-set on a sibling's terminal-changed signal.

## Purpose

To express "publisher X is committed to publish messages for instance Y on type Z." The row set is desired state: it is the source of truth for which publishers should be active at any time, and rimsky reconciles publisher-side state against it — a reconciliation worker continuously drives unmounted rows toward active, and a startup resync pass re-drives rows the publisher dropped. The instance surface exposes per-subscription state so an operator can observe mounting progress instead of inferring it from instance creation succeeding.

## Boundaries

Owns: the persisted publisher-subscription row, its per-publisher composite identity, the lifecycle state field (mounting / active / failed / stopped), the failure-reason field carried by failed rows, the message-type field, and the resolved-config blob. Delivery routes by message type against node-subscription edges; the subscription itself names a publisher and a type, not a receiver.

Does NOT own: the publisher's substrate state, the messages emitted (those are `concept:message`), or the publisher-side persistence of subscription state (each publisher owns its own state schema; see `concept:sensor`).

Adjacent: `concept:publisher` (the protocol), `concept:sensor` (one class of publisher implementation), `concept:message` (envelopes emitted under this subscription's authority), `concept:replica` (a publisher-subscription is per-name, not per-replica).

## Invariants

- Identity is composite over the owning publisher's name plus the subscription's own identifier — operators can scan one publisher's subscriptions efficiently.
- The declared message type must match an entry in the target instance's template message-schema registry at the publisher-subscription's mounting time; mismatches keep the row in the mounting state and the failed-reason field carries the diagnostic.
- The lifecycle state is one of mounting, active, failed, or stopped. Rows are created in the mounting state — instance creation never performs (or blocks on, or fails because of) the publisher subscribe handshake. A reconciliation worker drives the subscribe handshake for mounting rows with backoff and no attempt cap, flipping the row to active on success; the failed state is reserved for non-retryable errors (an unregistered publisher name, a config blob that fails resolution) and carries a reason; the stopped state is reached on unsubscribe. Startup resync re-drives mounting rows; it also recovers a failed row whose failure was an unregistered publisher name once that name is registered, flipping it back to mounting — other failed classes stay failed.
- The publisher capability check on the message-emit endpoint validates the subscription identifier against its instance and lifecycle state, accepting active and mounting rows (a fast publisher can emit its first message before the reconciler records the flip to active; rejecting it would drop a legitimate observation). Failed and stopped rows are rejected. Cross-instance subscription IDs are rejected as forbidden.
- invariant: message-inertness — rimsky-side subscription rows are inert with respect to the publisher's substrate. The row exists; the publisher's internal state is the publisher's concern.
