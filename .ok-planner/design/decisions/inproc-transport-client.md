---
decision: inproc-transport-client
---

# In-process executor client as a third transport on the client pool

## Choice

The in-process executor client is a third transport on the executor client pool's factory, alongside the gRPC and HTTP-bridge transports. Its client resolves handlers from the in-process registry by the canonical in-process executor identity, building the per-dispatch handler context per `decision:inproc-handler-interface`.

## Rationale

The executor-client interface and pool factory were designed for multiple transports (gRPC and HTTP-bridge are the existing pair). Inproc is a third instance of the same pattern. The runtime's dispatch call site is unchanged — transport stays opaque to dispatch.

## Alternatives

- Special-case in-process dispatch at the runtime's dispatch call site, bypassing the client pool — rejected: breaks transport opacity, adding a branch every dispatch-path change must then reason about.
