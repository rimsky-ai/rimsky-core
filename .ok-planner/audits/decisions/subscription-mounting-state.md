---
audit: subscription-mounting-state
artifact: decision:subscription-mounting-state
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:40Z
---

# Publisher subscriptions are desired-state rows with a visible lifecycle

Supported. `persistence.PublisherSubscriptionRow`'s state field is one of exactly four constants (`lib/foundation/persistence/publisher_subscriptions.go`: `mounting`, `active`, `failed`, `stopped`), matching the claimed state set. `lib/runtime/publishers.go::StartPublisherSubscriptionsForInstance` inserts every subscription row in the `mounting` state at instance-create and performs no inline Subscribe RPC, proven by `lib/control/config/publisher_reconciler_test.go::TestStartPublisherSubscriptions_InsertsMountingNoInlineRPC`, which asserts zero Subscribe calls happened synchronously. `handleGetInstance` (`lib/control/controlapi/instances.go`) lists each instance's subscription rows and exposes `state` (plus id, publisher name, kind, message type, started_at, failure_reason) on the instance-detail response, exercised by `lib/control/controlapi/instances_test.go`, which asserts a freshly created row surfaces as `state: mounting`.
