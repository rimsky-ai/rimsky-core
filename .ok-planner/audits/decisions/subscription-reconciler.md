---
audit: subscription-reconciler
artifact: decision:subscription-reconciler
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:40Z
---

# A reconciliation worker drives publisher Subscribe

Supported. `lib/runtime/publishers.go::RunPublisherSubscriptionReconciler` runs `reconcilePublisherSubscriptionsOnce` immediately, then again every tick of a fixed-interval ticker, forever (no attempt cap on the outer loop — a `Subscribe` that keeps failing is simply retried on the next tick indefinitely). `failed` is reached only for non-retryable causes: publisher-config resolution failure or an unregistered publisher name (`StartPublisherSubscriptionsForInstance`), never for a `Subscribe` RPC failure inside the reconciler loop. This is proven directly by `lib/control/config/publisher_reconciler_test.go::TestPublisherSubscriptionReconciler_RetriesPastBudgetThenActivates`, which fails the first four `Subscribe` attempts (past the inner `retryRPCWithBackoff`'s 3-attempt-per-cycle budget) and asserts the row still reaches `active` on a later reconcile pass rather than `failed`. The startup resync pass and the periodic pass are the same function (`ResyncPublisherSubscriptions`, invoked both by `reconcilePublisherSubscriptionsOnce` and thus by the ticker's first immediate call); it lists each publisher's live subscriptions via `ListSubscriptions` and, for any expected row already present in that live set, only flips `mounting`→`active` and `continue`s without re-issuing `Subscribe` — never re-subscribing an already-active row. `TestStartControlAPI_StartsSubscriptionReconciler` confirms the reconciler is wired into control-API startup.
