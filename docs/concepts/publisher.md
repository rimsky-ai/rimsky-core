# Publisher

A publisher is a peer service that publishes messages into rimsky. Publishers implement the Publisher gRPC protocol (four verbs: `Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`) and POST message envelopes to the generic `POST /instances/{id}/messages` endpoint with `sender_kind: "publisher"` and a `publisher_subscription_id` capability token.

Publishers are peer-services in the same trust perimeter as executors and claim-producers: out-of-process, gRPC-addressed at startup via the `publishers:` block of `rimsky.yml`, and exclusively responsible for their own state and HA posture.

## Bundled implementations

Four reference impls ship with rimsky under `sensors/sensor-*/`:

- `sensor-cron` — cron-expression firing (replaces the retired per-node `schedule:` field).
- `sensor-http` — HTTP-poll with body-hash watermark.
- `sensor-object-store` — object-store list with `name` or `last_modified` watermark.
- `sensor-webhook` — inbound webhook receiver.

All four are sensors at the binary boundary (`sensors/sensor-*/`), publishers at the wire boundary (Publisher protocol).

## Wire path

1. Operator registers a template with a `publishers:` block listing one or more publisher entries (name + kind + per-kind config + routing fields `target_node` + `message_kind`).
2. Operator creates an instance.
3. Rimsky calls `Publisher.Subscribe` on the addressed publisher for each block entry; the publisher accepts the subscription and starts watching.
4. On observation, the publisher POSTs a message envelope to `POST /instances/{id}/messages` with `sender_kind: "publisher"` + the subscription id.
5. Rimsky validates the capability token (subscription is active for this instance), records the message, and delivers it at the next frame boundary.
6. Operator terminates the instance → rimsky calls `Publisher.Unsubscribe`.

## Idempotency

Publishers should POST with an `Idempotency-Key` HTTP header. Rimsky dedups on `(instance_id, sender, idempotency_key)` for 24h (configurable). Replays return the original `message_id` with `200 OK`. Bundled publishers compose keys per fire (cron: `{subscription_id}+{fire_window}`; http: `{subscription_id}+{body_sha256}`; object-store: `{subscription_id}+{object_etag}`; webhook: `{subscription_id}+{header_value}`).

## Replicas

Single-replica is the v1 contract for bundled sensors. Multi-replica HA is the publisher implementation's concern, not rimsky's.

See also: [publisher-subscription](publisher-subscription.md), [sensor](sensor.md), [replica](replica.md), [Publisher protocol guide](../protocols/publisher.md).
