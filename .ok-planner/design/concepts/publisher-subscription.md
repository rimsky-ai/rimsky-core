---
concept: publisher-subscription
status: as-is
aliases: [sensor-watch]
---

# Publisher-subscription

## Definition

A publisher-subscription is the rimsky↔publisher binding state for one (instance, publisher, kind) triple. Created at instance creation when the template's publishers block declares a publisher entry; lives in a persisted publisher-subscription ledger; identified by `publisher_subscription_id` (UUIDv4); dropped at instance termination.

A publisher-subscription is the rimsky-side mirror of the publisher's per-binary state. The publisher holds the substrate-specific state (cron schedule, body hash, watermark cursor); rimsky holds the binding metadata (which publisher, which instance, which kind, which target node, which message kind).

## Naming note

Named `publisher-subscription` rather than `subscription` because `concept:node-subscription` already owns the receiver-side template-DSL `subscribes:` block. The two are orthogonal: a publisher-subscription is a publisher↔rimsky binding; a node-subscription is one template node's wait-set on a sibling's terminal-changed signal.

## Purpose

To express "publisher X is committed to publish messages for instance Y on kind Z." The row set is desired state: it is the source of truth for which publishers should be active at any time, and rimsky reconciles publisher-side state against it — a reconciliation worker continuously drives unmounted rows toward active, and a startup resync pass re-drives rows the publisher dropped. The instance surface exposes per-subscription state so an operator can observe mounting progress instead of inferring it from instance creation succeeding.

## Boundaries

Owns: the persisted publisher-subscription row, the (publisher_name, id) primary key, the lifecycle state field (`mounting` | `active` | `failed` | `stopped`), the failure-reason field carried by failed rows, the inline routing fields (`target_node`, `message_kind`), and the resolved-config blob.

Does NOT own: the publisher's substrate state, the messages emitted (those are `concept:message`), or the publisher-side persistence of subscription state (each publisher owns its own state schema; see `concept:sensor`).

Adjacent: `concept:publisher` (the protocol), `concept:sensor` (one class of publisher implementation), `concept:message` (envelopes emitted under this subscription's authority), `concept:replica` (a publisher-subscription is per-name, not per-replica).

## Invariants

- Primary key is `(publisher_name, id)` — operators can scan one publisher's subscriptions efficiently.
- `target_node` is `NOT NULL`. Publishers without a target_node fail at template registration.
- `message_kind` defaults to `"invalidate"` when the publisher-spec omits it.
- `state` is one of `mounting`, `active`, `failed`, `stopped`. Rows are created in `mounting` — instance creation never performs (or blocks on, or fails because of) the publisher Subscribe handshake. A reconciliation worker drives the Subscribe handshake for mounting rows with backoff and no attempt cap, flipping the row to `active` on success; `failed` is reserved for non-retryable errors (a publisher name not present in the registry, a config blob that fails resolution) and carries a reason; `stopped` on unsubscribe. Startup resync re-drives `mounting` rows; it also recovers a `failed` row whose failure was an unregistered publisher name once that name is registered, flipping it back to `mounting` — other `failed` classes stay failed.
- The publisher capability check on the message-emit endpoint validates `(id, instance_id, state)` — three-way match, accepting `active` and `mounting` (a fast publisher can emit its first message before the reconciler records the flip to active; rejecting it would drop a legitimate observation). `failed` and `stopped` rows are rejected. Cross-instance subscription IDs are rejected with 403.
- @blessed-invariant: rimsky-side subscription rows are inert with respect to the publisher's substrate. The row exists; the publisher's internal state is the publisher's concern.
