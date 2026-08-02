---
audit: executor-trace-observability
artifact: story:executor-trace-observability
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:36Z
---

# Operator queries/streams executor traces

Supported. The executor observability protocol declares both a fetch verb (returns a completeness flag plus the recorded event list for a dispatch id) and a streaming verb (delivers events live and closes with a terminal marker). A shared in-process trace store implements both, plus an HTTP+JSON/SSE bridge for browser dashboard clients, and both bundled executors that advertise trace support wire it in. The executor conformance suite drives both RPCs against a live canned dispatch: it asserts the fetch verb returns the recorded events with completeness true once the dispatch has settled, asserts the streaming verb delivers events live and terminates with the completion marker, asserts the correctly-shaped empty response for an unknown dispatch id, and asserts post-retention eviction — covering both the after-the-fact and while-in-flight halves the story names.
