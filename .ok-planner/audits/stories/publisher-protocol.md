---
audit: publisher-protocol
artifact: story:publisher-protocol
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:57Z
---

# Service author writes a custom publisher/sensor with restart-safe subscriptions

Supported. `publisher.proto` defines the `Publisher` service (`Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`) that both custom publishers and the four bundled sensors (`sensor-cron`, `sensor-http`, `sensor-object-store`, `sensor-webhook`, all present under `lib/services/sensors/`) implement; a conformance runner (`lib/protocols/conformance/publisher/runner.go`, with its own `runner_test.go`) lets a service author validate a custom implementation. Rimsky drives subscription lifecycle at instance create/terminate (`lib/runtime/publishers.go::StartPublisherSubscriptionsForInstance`/`StopPublisherSubscriptionsForInstance`) and reconciles on a running loop and at restart via `ResyncPublisherSubscriptions`, which calls each publisher's `ListSubscriptions` and only issues `Subscribe` for subscriptions absent from the live set — subscriptions already live are left alone, not re-issued. This resync behavior is directly exercised by `lib/control/config/publisher_resync_test.go` (using a fake publisher client that records `Subscribe`/`Unsubscribe`/`ListSubscriptions` calls) plus `lib/runtime/publisher_reconciler_tick_test.go` and `lib/runtime/publisher_lifecycle_invariants_test.go`. Delivered messages land through the generic `POST /instances/{id}/messages` endpoint (`lib/control/controlapi/messages.go`), consistent with the proto's documented delivery path.
