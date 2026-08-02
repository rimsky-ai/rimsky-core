---
audit: blob-backend
artifact: decision:blob-backend
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:53Z
---

# The filesystem blob backend, rooted under the per-run artifact layout, with the default spill threshold

Supported. `cmd/rimsky/cli/compose/synthetic_config.go` (tagged `@decision: blob-backend`) sets the one-shot self-hosting run's `persistence.blob.backend` to `"filesystem"` rooted at `<run-dir>/blobs`, leaving `spill_threshold_bytes` unset so the loader's `persistence.DefaultBlobConfig()` fallback (65536 bytes) applies. The filesystem backend (`lib/foundation/persistence/blob_filesystem.go`) writes spilled values as sibling files under its root; `ShouldSpillBlob` (`blob_spill.go`) keeps values at or under the threshold out of any non-inline backend's `Write` path so they stay inline in the SQL row, and only routes larger values to spill. `InlineBackend.Write` (`blob_inline.go`) always errors, confirming the inline alternative's described all-or-nothing behavior.
