---
audit: subscription-mounting
artifact: story:subscription-mounting
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:40Z
---

# Operator observes publisher subscriptions mount

Supported. `lib/runtime/publishers.go::StartPublisherSubscriptionsForInstance` inserts each declared publisher subscription in the `mounting` state at instance-create time with no inline Subscribe RPC, confirmed by `lib/control/config/publisher_reconciler_test.go::TestStartPublisherSubscriptions_InsertsMountingNoInlineRPC`. `handleGetInstance` in `lib/control/controlapi/instances.go` lists each instance's publisher-subscription rows and surfaces `id`, `publisher_name`, `kind`, `message_type`, `state`, `started_at`, and `failure_reason` on the instance-detail response, exercised by `lib/control/controlapi/instances_test.go`. The reconciler (`RunPublisherSubscriptionReconciler` / `ResyncPublisherSubscriptions`) drives mounting rows to `active` without operator action and is proven to run past its per-cycle retry budget by retrying on the next tick in `lib/control/config/publisher_reconciler_test.go::TestPublisherSubscriptionReconciler_RetriesPastBudgetThenActivates`, and to be started automatically by the control API in `TestStartControlAPI_StartsSubscriptionReconciler`. `examples/subscription-mounting-demo.sh` (carrying the story's own citation) additionally drives the full story end to end against real Docker images: immediate 201 on create, an observably `mounting` subscription while the publisher is paused, and an unattended flip to `active` once it wakes.
