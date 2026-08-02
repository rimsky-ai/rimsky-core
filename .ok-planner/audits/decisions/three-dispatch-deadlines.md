---
audit: three-dispatch-deadlines
artifact: decision:three-dispatch-deadlines
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:36Z
---

# Three orthogonal dispatch deadlines

Supported. The node-template schema carries all 3 named fields (sync-RPC deadline, max-quiet-period, max-runtime) as independent, per-node string durations, each resolved with its own deployment-config default when the node leaves it unset; a zero-duration value resolves and is honored as the disable sentinel for every one of the 3, distinct from an unset field falling back to its default. The sync-RPC deadline is applied to the executor dispatch call as the sole outbound-call deadline: the supervisor's dispatch path wraps the call in exactly one context timeout derived from this value, and the bundled executors' own outbound HTTP clients used to perform the executor's work are constructed with no client-side timeout of their own, relying on the propagated deadline rather than adding a second, lower ceiling — checked across the 2 bundled executors that make outbound calls as part of dispatch (http-node, and claude-agent's callback/cancel-probe clients, which serve a different purpose than the dispatch call itself and are unrelated to the sole-bound claim). The quiet-period and runtime deadlines feed a separate deadline sweep that reaps a dispatch exceeding either, independently of the sync deadline.
