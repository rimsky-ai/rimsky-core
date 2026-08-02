---
audit: executor-protocol
artifact: story:executor-protocol
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:36Z
---

# Service author writes custom executor against a self-contained protocol

Supported. The executor protocol's unary dispatch verb and its optional observability handshake (a `Capabilities` call returning an attribute JSON Schema, a declared-tags set, and a declared-error-classes set) are both defined on the wire. On rimsky's side, discovery happens at both control-API and supervisor startup by probing every configured executor's capabilities; template registration validates each node's attribute schema against the probed schema and range-checks each operator error-policy key against the probed error classes; and the dispatch path rejects any settling outcome's tags that are not in the probed declared-tags set. All 4 bundled executors (claude-agent, http-node, verifier-http, verifier-shape-checks) implement both the dispatch and observability services, and a dedicated executor conformance suite drives dispatch, terminal-outcome acceptance, and the observability handshake against any executor under test, independent of rimsky's own internals — matching the story's "plugs into a rimsky stack without rimsky-internal knowledge."
