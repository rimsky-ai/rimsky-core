---
decision: inproc-transport-client
status: as-is
aliases: []
---

# In-process executor client as a third transport on the client pool

## Choice

Add an in-process case as a third transport on the executor client pool's factory, alongside the existing gRPC and HTTP-bridge transports. A new in-process client constructor takes an executor endpoint plus an in-process registry and returns a client whose execute method looks up the handler in the registry by the in-process executor identity (the endpoint URL, e.g. the canonical in-process URL for the loop-counter kind). The pool keys clients by transport, TLS mode, and URL as today — the TLS-mode key segment is unused for the in-process transport but slots cleanly into the key.

## Rationale

The existing executor-client interface and pool factory were designed for multiple transports (gRPC and HTTP-bridge are the existing pair). Inproc is a third instance of the same pattern. The runtime's dispatch call site is unchanged — transport stays opaque to dispatch.
