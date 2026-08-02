---
audit: deposit-settle-window
artifact: decision:deposit-settle-window
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:26Z
---

# Optional per-watch mtime-quiescence settle window holds mid-write deposits, backend-agnostically, default off

Supported. `lib/services/sensors/sensor-object-store/sensor.go::pollOne` computes `now.Sub(o.LastModified) < cur.SettleWindow` per object on every poll and, when true, `continue`s without publishing and without marking the object seen or advancing the watermark, so an unsettled object is reconsidered on the next poll and published once quiet for the full window; the check sits once in the shared poll loop ahead of any backend-specific lister call (`filesystem_lister.go`, `memory_lister.go` both just implement `ObjectLister.List`), and `Subscribe` defaults `SettleWindow` to `0` (disabled) unless `resolved_config.settle_window` is set. `TestTick_SettleWindowHoldsUnsettledObjectUntilQuiet` (`lib/services/sensors/sensor-object-store/sensor_test.go`) exercises the hold-then-publish behavior end to end against the tick loop, and `TestSubscribe_RejectsNegativeSettleWindow` covers the input validation.
