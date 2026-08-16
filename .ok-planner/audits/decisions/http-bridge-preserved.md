---
audit: http-bridge-preserved
artifact: decision:http-bridge-preserved
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# The claude-agent's HTTP-JSON bridge beside gRPC, and which deployments it serves

Supported. The executor mounts an HTTP bridge carrying exactly the four route groups the decision names: a health route, an execute route that accepts the dispatch request as JSON, and the observability capabilities route plus the mounted trace routes. The execute route parses the JSON body into the same request message the gRPC surface takes, mints an ack id, runs the dispatch asynchronously with that ack id stamped onto the eventual callback, and replies immediately with the await-async outcome carrying the ack id, which is the async-ack contract the decision describes. The listener binds the host and port from the executor's options, so the port is configurable. The bridge starts from exactly one place — the standalone serve path — and nothing else in the tree calls it; the in-process path builds the executor server directly from the bundled registration entrypoint and invokes its handler method, touching neither the HTTP bridge nor a gRPC listener, so the standalone-only scoping holds. Seven tests cover the bridge: health, execute acking and posting its callback, the response matching the gRPC outcome shape, unknown-field tolerance matching the gRPC transport, oversized-body rejection, the capabilities route, and a trace read after a dispatch.
