---
audit: producer-error-passthrough
artifact: story:producer-error-passthrough
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# The control-api response carries the producer's error class and message

Supported. `writeError` (`lib/control/controlapi/app.go`, annotated `@decision: producer-error-passthrough`) unwraps any `*peer.ProducerCallError` (via `errors.As`, so wrapped errors are caught too) and calls `writeProducerError`, which writes `error_class`, `message`, and `producer_name` into the JSON body under HTTP 422 for a producer-side rejection (InvalidArgument/FailedPrecondition/OutOfRange/NotFound/AlreadyExists/PermissionDenied) or 502 otherwise — distinguishing producer rejection from a generic internal error as the story requires. `writeError` is the single centralized error writer used at all 117 `writeError(...)` call sites across the control-api handlers (e.g. the asset-delete path in `assets.go`, which reaches it when a producer's Release call fails during an API-triggered delete), and is unit-tested directly for the 502/422 split, class/message propagation, wrapped-error unwrapping, and the unaffected-internal-error case (`lib/control/controlapi/app_producer_error_test.go`, 5 test functions).
