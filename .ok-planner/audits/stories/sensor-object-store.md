---
audit: sensor-object-store
artifact: story:sensor-object-store
determination: supported
commit: b767a27d
audited: 2026-08-02T09:29:02Z
---

# Workflow reacts to content deposited into an operator-designated location

Supported. `lib/services/sensors/sensor-object-store` subscribes an operator-declared bucket/prefix against the registered `filesystem` backend (`FilesystemLister` walks a bucket directory under a configured root, treating first-level directories as buckets and files as objects), polls on an interval, and emits one message per newly observed object — no custom integration code, only declarative `resolved_config`. New-object detection is watermark-based (by name or by last-modified) with durable seen-name/watermark state in Postgres surviving restart (`TestSubscribe_RestartReplay_PreloadsWatermark`, `TestSeenNames_PersistAndRoundTripAcrossRestart`, `TestStateDB_PersistsAcrossRestart`) and per-object idempotent delivery keyed on name+ETag (`TestTick_EmitsOneMessagePerNewObject`, `TestTick_SameETagDifferentObjectsBothDeliveredWithDistinctIdempotencyKeys`).
