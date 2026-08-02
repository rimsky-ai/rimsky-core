---
audit: blob-spill-threshold-config
artifact: decision:blob-spill-threshold-config
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:29Z
---

# Per-deployment configurable spill threshold, inline default

`ShouldSpillBlob` in `lib/foundation/persistence/blob_spill.go` implements the threshold check (never spills for the "inline" backend, never spills below/at threshold, no-op on a non-positive threshold), and `BlobConfig.SpillThresholdBytes` is wired from the `persistence.blob.spill_threshold_bytes` YAML key through `lib/control/config/claim_producers.go` into `OpenBlobBackend`/`SetBlobBackend`, so an operator can tune the cutoff without a code change. `DefaultBlobConfig` sets `Backend: "inline"`, and inline is explicitly exempted from spilling in `ShouldSpillBlob`, matching the claimed all-inline default. `TestShouldSpillBlobDecision` (table test in `blob_roundtrip_test.go`) and `TestValidateBlobConfig` (`blob_test.go`, including a negative-threshold rejection case) exercise the decision function and its config validation.
