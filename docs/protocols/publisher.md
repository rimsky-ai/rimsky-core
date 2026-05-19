# Publisher protocol guide

The Publisher protocol is the rimsky-facing surface that peer services implement to publish messages into rimsky. Four verbs; one message endpoint.

## Verbs

| Verb | Direction | Purpose |
|---|---|---|
| `Capabilities` | rimsky → publisher | Advertise supported kinds + protocols. |
| `Subscribe` | rimsky → publisher | Start a publisher-subscription for one (instance, kind) pair. |
| `Unsubscribe` | rimsky → publisher | Stop a publisher-subscription. |
| `ListSubscriptions` | rimsky → publisher | Reconcile-on-startup; rimsky compares its expected set against the publisher's reported set. |

The publisher emits messages by POSTing message envelopes to the generic `POST /instances/{id}/messages` endpoint with `sender_kind: "publisher"` + `publisher_subscription_id` capability token. There is no special observation-deposit route.

## Subscribe payload

```protobuf
message SubscribeRequest {
  string publisher_subscription_id = 1;
  string instance_id               = 2;
  string kind                      = 3;
  bytes  resolved_config           = 4;  // per-instance config; substituted from template
  string target_node               = 5;  // receiver node alias; copied onto messages
  string message_kind              = 6;  // default "invalidate"; copied onto messages
}
```

The publisher persists `target_node` and `message_kind` alongside the subscription state; at fire time the publisher constructs `{kind: message_kind, target: target_node, ...}` message envelopes from these inline routing fields.

## Message-envelope shape at emit time

```json
{
  "kind": "invalidate",
  "target": "tick",
  "payload": <raw observation bytes>,
  "sender": "sensor-cron",
  "sender_kind": "publisher",
  "publisher_subscription_id": "8a4f...uuid"
}
```

POST to `/instances/{instance_id}/messages` with `Idempotency-Key` header for at-most-once semantics.

## Rimsky-side capability check

Rimsky validates `(publisher_subscription_id, instance_id, state='active')` is a live row in `rimsky_publisher_subscriptions` before inserting the message. Cross-instance subscription IDs return `403 Forbidden`. The request's `sender` field is ignored — rimsky derives `sender` from the subscription row's `publisher_name`.

## Conformance

A black-box conformance suite lives at `cmd/rimsky-publisher-conformance/`. Point it at any Publisher implementation to verify lifecycle + emit shape:

```sh
rimsky-publisher-conformance --endpoint grpc://my-publisher:9100 \
                             --kind cron \
                             --resolved-config '{"cron":"* * * * *"}' \
                             --instance-id <uuid>
```

## Bundled implementations

- `sensors/sensor-cron` — cron firing.
- `sensors/sensor-http` — HTTP-poll with body-hash watermark.
- `sensors/sensor-object-store` — object-store list with `name` or `last_modified` watermark.
- `sensors/sensor-webhook` — inbound webhook receiver.

See [publisher](../../.ok-planner/design/concepts/publisher.md), [publisher-subscription](../../.ok-planner/design/concepts/publisher-subscription.md), [sensor](../../.ok-planner/design/concepts/sensor.md).
