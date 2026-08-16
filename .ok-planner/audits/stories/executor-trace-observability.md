---
audit: executor-trace-observability
artifact: story:executor-trace-observability
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:51:57Z
---

# A dashboard streams an executor's trace live and reads the finished record back

Supported. Driven through the public surface against a released-image stack with
the bundled HTTP-node executor wired in as an ordinary gRPC executor, using a
dashboard-shaped client built for the run whose only rimsky dependency is the
published protocols module, and an endpoint that holds the executor's request
open so the dispatch is provably in flight rather than presumed so. Twenty
checks, none failing. Discovery worked from the control API: it named the
executor's observability endpoint and advertised both trace fetch and trace
streaming, and the same two flags came back over the protocol itself. With the
request held, the first streamed event arrived while the fetched trace was still
incomplete, no terminal event had streamed and the request was still held;
releasing the endpoint let the same open stream carry the remaining events in
order. Fetched afterwards, the trace named the dispatch the event feed had
named, came back complete and not evicted, and carried both events as structured
records — every one with event id, timestamp, severity, category and message,
one with machine-readable attributes, and the completion naming its parent. An
unknown dispatch read back as evicted rather than erroring.
