---
audit: single-process-all-in-one
artifact: story:single-process-all-in-one
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:37Z
---

# Operator gets one process for scheduler + supervisor + control-api, memory blob included

Supported. `launch.StartUnifiedStack` starts scheduler, supervisor, and control-api as three in-process runners sharing one opened blob backend and one persistence driver, and `TestStartUnifiedStack_BlobBackendOpenedOnceAndSharedAcrossRunners` asserts the memory blob backend is opened exactly once and the identical instance is handed to all three runners — the concrete mechanism behind "the memory blob backend working there, because the roles actually share a process." The memory backend is additionally gated to require the unified topology (`OpenBlobBackend` errors outside it, per `TestOpenBlobBackend_MemoryGatedByTopology` and `TestLoadRimskyConfig_Blob_MemoryBackendRequiresUnifiedRole`), so the story's promise is enforced, not merely incidental.
