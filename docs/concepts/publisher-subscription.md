# Publisher-subscription

A publisher-subscription is the rimsky↔publisher binding state for one (instance, publisher, kind) triple. Created at instance creation; lives in `rimsky_publisher_subscriptions`; identified by `publisher_subscription_id` (UUIDv4); dropped at instance termination.

Each row carries:

- `publisher_subscription_id` — UUIDv4, the capability token used at the messages endpoint.
- `instance_id` — the receiving instance.
- `publisher_name` — the publisher peer (from the operator's `rimsky.yml`).
- `kind` — the publisher's kind (e.g. `cron`, `http`).
- `resolved_config` — the per-instance config (after `{{params.X}}` substitution).
- `target_node` — receiver node alias; the publisher copies this onto every emitted message envelope as `target`.
- `message_kind` — the message kind (default `invalidate`); the publisher copies this onto every emitted envelope as `kind`.
- `state` — `active`, `failed`, or `stopped`.

The row is the source of truth for which publishers should be active. Rimsky reconciles publisher-side state against the row set at supervisor startup via `runtime.ResyncPublisherSubscriptions`.

## Naming

Named `publisher-subscription` rather than `subscription` to disambiguate from `node-subscription` (the receiver-side template-DSL `subscribes:` block declared on individual nodes). The two are orthogonal — a publisher-subscription is a publisher↔rimsky binding; a node-subscription is one template node's wait-set on a sibling's terminal-changed signal.

## Capability check at the messages endpoint

Publishers POSTing to `/instances/{id}/messages` with `sender_kind: "publisher"` must include the `publisher_subscription_id` field on the body. Rimsky validates `(id, instance_id, state='active')` is a live row in `rimsky_publisher_subscriptions`; mismatch returns `403 Forbidden`. Cross-instance subscription IDs are rejected. The request's `sender` field is ignored — rimsky derives `sender` from the row's `publisher_name`.

See also: [publisher](publisher.md), [sensor](sensor.md), [message](https://github.com/fallguy/rimsky/blob/main/.ok-planner/design/concepts/message.md).
