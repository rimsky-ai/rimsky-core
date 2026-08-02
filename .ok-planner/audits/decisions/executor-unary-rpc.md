---
audit: executor-unary-rpc
artifact: decision:executor-unary-rpc
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:36Z
---

# Execute is unary

Supported. The dispatch RPC is declared unary request-in, unary response-out — no streaming keyword on either side — and its response type is exactly the four-variant oneof the decision names: success, error, park, await-async-callback. The supervisor's dispatch path calls this RPC once per attempt under a single deadline, with async liveness and attribute writes carried by separate HTTP callback routes rather than an in-band stream, matching the rationale that nothing in a dispatch actually streams.
