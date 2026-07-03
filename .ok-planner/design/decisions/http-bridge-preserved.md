---
decision: http-bridge-preserved
status: as-is
aliases: []
---

# The Go claude-agent preserves the HTTP-JSON bridge surface

## Choice

The Go claude-agent includes an HTTP-JSON bridge equivalent to the retired TypeScript implementation's, on the same port env var: a health endpoint, an execute endpoint accepting the JSON dispatch body and replying with an async ack id, and the observability capability/trace routes. Callers dispatching via HTTP-JSON instead of gRPC continue to work; the bridge fires only in standalone deployments (in-process dispatch bypasses both transport surfaces).

## Rationale

External callers depending on the HTTP surface should not have to change protocols for the Go port.

## Alternatives

Drop the HTTP bridge — rejected: breaking change without benefit.
