---
audit: executor-trace-observability
artifact: story:executor-trace-observability
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# Traces stream while a dispatch runs and read back whole when it is done

Supported. A dashboard-side client built against the protocols module alone
learned the executor's observability endpoint and its trace-get and trace-stream
support from the control API, learned the dispatch id from the event feed, and
then talked the protocol itself. With the dispatch held open on purpose — the
node's HTTP call blocked on an endpoint the run releases — the stream delivered
the executor's start event at a moment the fetched trace still reported
incomplete, no terminal event streamed and the request still held; releasing it
carried start, completion and the completion sentinel down the same open stream
in order. Fetched afterwards, the trace named the dispatch, came back complete
and not evicted, carried both events, and every record was structured rather
than a log line — event id, timestamp, severity, category and message on each,
machine-readable attributes on one, and a parent link on the child. An unknown
dispatch read back as evicted rather than erroring.
