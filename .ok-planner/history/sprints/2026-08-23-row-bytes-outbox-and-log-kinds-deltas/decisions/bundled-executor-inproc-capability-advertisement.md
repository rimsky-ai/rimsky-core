---
decision: bundled-executor-inproc-capability-advertisement
---

# Bundled in-proc handlers advertise capabilities into the discovery cache at registration time

## Choice

Each bundled executor handler package exports its expected-attributes schema, declared tags, and declared error classes at package scope. The bundled registration entrypoint populates the discovery cache with these at registration time, bypassing the gRPC observability handshake; bundled claim producers advertise the capabilities their handler reports at construction. The same package-scope accessors feed the standalone gRPC observability handshake response, so both modes advertise from one definition. Discovery-cache entries created this way are marked static: the periodic re-probe loop skips them, since there is no endpoint to probe and the handler cannot become unreachable within its own process.

## Rationale

In-proc handlers receive no gRPC handshake; without an explicit advertisement path the discovery cache is empty and per-node schema validation cannot dispatch. Deriving both modes' advertisements from the same package-scope accessors keeps them equivalent by construction. The static marker exists because the refresh loop otherwise treats every cache entry as a remote service and would mark in-proc entries unreachable, wiping their capabilities.

## Alternatives

- Run a loopback gRPC handshake for each in-proc handler — rejected: reintroduces the overhead in-proc dispatch is meant to eliminate.
- Duplicate the schema/tags/classes literals between the handshake response and the registration path — rejected: the two modes would drift.
- Let the refresh loop probe in-proc entries — rejected: there is nothing to probe; the loop would destroy valid advertisements.
