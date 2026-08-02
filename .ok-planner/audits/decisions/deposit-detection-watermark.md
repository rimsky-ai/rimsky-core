---
audit: deposit-detection-watermark
artifact: decision:deposit-detection-watermark
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:26Z
---

# Object-store sensor detects deposits by polling against a durable watermark/seen-set, at-least-once with idempotent keys

Supported. `lib/services/sensors/sensor-object-store/sensor.go::pollOne` lists the watched location on each tick, compares each object against the in-memory watermark/seen-set (`isNewObject`), builds an idempotency key as `subscriptionID+objectName+etag`, and only advances the watermark/seen-set (`markObjectSeen` plus the corresponding `stateDB` write) after a successful (non-transiently-failed) publish; the watermark and seen-set are persisted to a durable `stateDB` (`state_db.go`) and reloaded on `AttachStateDB`/`Subscribe`. Direct unit tests cover every clause: `TestTick_FailedPostDoesNotAdvanceWatermark` (a transient publish failure leaves the watermark untouched), `TestTick_PermanentRejectionDropsObject_AdvancesWatermarkAndContinues` (a rejected publish still advances, i.e. deliberate at-least-once with downstream dedup), `TestTick_IdempotencyKeyIncludesObjectNameWhenETagEmpty` and `TestTick_SameETagDifferentObjectsBothDeliveredWithDistinctIdempotencyKeys` (key derivation), and `TestSubscribe_RestartReplay_PreloadsWatermark` plus `TestStateDB_PersistsAcrossRestart` (durability across restarts) — all in `lib/services/sensors/sensor-object-store/sensor_test.go` and `state_db_test.go`.
