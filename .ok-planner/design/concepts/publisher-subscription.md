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

To express "publisher X is committed to publish messages for instance Y on kind Z." The row is the source of truth for which publishers should be active at any time; rimsky reconciles publisher-side state against this row set at supervisor startup via a resync pass.

## Boundaries

Owns: the persisted publisher-subscription row, the (publisher_name, id) primary key, the lifecycle state field (`active` | `failed` | `stopped`), the inline routing fields (`target_node`, `message_kind`), and the resolved-config blob.

Does NOT own: the publisher's substrate state, the messages emitted (those are `concept:message`), or the publisher-side persistence of subscription state (each publisher owns its own state schema; see `concept:sensor`).

Adjacent: `concept:publisher` (the protocol), `concept:sensor` (one class of publisher implementation), `concept:message` (envelopes emitted under this subscription's authority), `concept:replica` (a publisher-subscription is per-name, not per-replica).

## Invariants

- Primary key is `(publisher_name, id)` — operators can scan one publisher's subscriptions efficiently.
- `target_node` is `NOT NULL`. Publishers without a target_node fail at template registration.
- `message_kind` defaults to `"invalidate"` when the publisher-spec omits it.
- `state` is one of `active`, `failed`, `stopped`. Transitions: `active → failed` on Subscribe RPC failure (operator-recoverable via resync); `active → stopped` on Unsubscribe success; `failed → active` on resync re-Subscribe.
- The publisher capability check on the message-emit endpoint validates `(id, instance_id, state='active')` — three-way match. Cross-instance subscription IDs are rejected with 403.
- @blessed-invariant: rimsky-side subscription rows are inert with respect to the publisher's substrate. The row exists; the publisher's internal state is the publisher's concern.
