---
decision: http-bridge-preserved
status: as-is
aliases: []
---

# The claude-agent carries an HTTP-JSON bridge alongside gRPC

## Choice

The claude-agent executor exposes an HTTP-JSON bridge alongside its gRPC surface: a health endpoint, an execute endpoint accepting the JSON dispatch body and replying with an async ack id, and the observability capability/trace routes, on a configurable port. The bridge serves only standalone deployments; in-process dispatch bypasses both transport surfaces.

## Rationale

Callers that dispatch over HTTP-JSON keep a working surface without adopting gRPC.

## Alternatives

- Serve gRPC only — rejected: breaks HTTP-JSON callers for no benefit.
