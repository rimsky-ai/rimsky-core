---
audit: lineage-subscriber-poller
artifact: decision:lineage-subscriber-poller
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:57Z
---

# OpenLineage subscriber emits by polling, not by push-style lifecycle subscription

Supported. `lib/services/subscribers/openlineage/subscriber.go::Run` (annotated `@decision: lineage-subscriber-poller`) is a plain ticker loop calling `tick`, which reads `rimsky_lineage` rows newer than a durably-persisted cursor (`fetchSince`/`advanceCursor`/`persistCursor` against a `rimsky_openlineage_cursor` table) and forwards them; the subscriber does not implement or register the `lifecycle.LifecycleSubscriber` gRPC interface anywhere in the codebase (confirmed by grep: no `OnTemplateRegistered`/`OnInstanceCreated`/etc. implementation in the openlineage package). Cursor persistence and restart-resume behavior are covered by `subscriber_test.go`.
