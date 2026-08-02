---
audit: producer-error-passthrough
artifact: decision:producer-error-passthrough
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# writeError recognizes producer errors and carries class/message under a distinguishing status

Supported — this is the code-level twin of `story:producer-error-passthrough`, backed by the same evidence. `writeError` in `lib/control/controlapi/app.go` is explicitly annotated `@decision: producer-error-passthrough` at the `writeProducerError` function it delegates to on `errors.As(err, &peer.ProducerCallError{})`; the response body always carries `error_class`, `message`, and `producer_name`, and the HTTP status is 422 for a producer rejection (InvalidArgument/FailedPrecondition/OutOfRange/NotFound/AlreadyExists/PermissionDenied) versus 502 for any other producer-side failure, giving the "status distinguishing producer rejection from rimsky internal error" the choice specifies. `lib/control/controlapi/app_producer_error_test.go` unit-tests all five named scenarios (502 with class+message, 422 for each of the three rejection codes, unclassed failure, wrapped-error unwrapping, and the internal-error case staying unaffected).
