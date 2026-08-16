---
audit: async-callback-post-json
artifact: decision:async-callback-post-json
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:03:38Z
---

# The async-callback transport is an HTTP POST of a JSON outcome body keyed by ack id on the route path

Supported. The supervisor's callback server registers exactly one callback route — an HTTP POST whose final path segment is the async-acknowledgement identifier — and the handler reads the request body as JSON, correlating the dispatch by that path parameter against an in-memory registry with a persisted-row fallback so a restarted supervisor still resolves it. The callback server registers three POST routes and one health route in total; none is a gRPC surface, and the supervisor exposes no gRPC callback service anywhere, so the rejected alternative is genuinely absent rather than merely unused. The rationale's shared-code claim holds: the JSON body parses into the same internal terminal-event value the synchronous Execute path produces, and both feed the same terminal-application and persistence functions. The transport is exercised end to end by scenario tests that post real callback bodies at the running supervisor's callback address, including a restart-recovery case that proves correlation survives the process, and by the bundled executors and conformance runners that dial the route from the client side.
