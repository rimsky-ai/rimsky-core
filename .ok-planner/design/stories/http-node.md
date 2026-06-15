---
story: http-node
status: as-is
---

# Template author integrates HTTP upstreams

## Role

As a template author wiring a node against an upstream HTTP API, I can use the bundled HTTP-node executor to issue requests, route the response into the node's output attributes, opt into rate-limit parking on the 429 status code (auto-resume from the upstream's retry-after directive), and configure which JSON field on an upstream error body carries the error class with a stable fallback when absent, so that I integrate with HTTP upstreams without writing a custom executor.

## Capability

Bundled HTTP-node executor: HTTP request dispatch with response routing into node output attributes; rate-limit parking on the 429 status code honoring the upstream's retry-after directive; configurable error-class field on 4xx with a stable unspecified-class fallback.

## Business value

Template authors integrate with HTTP upstreams without writing a custom executor; the bundled executor handles rate limits and error classification natively.

## Acceptance

A template using the HTTP-node executor against a real upstream: a 200 response populates the node's output attributes from the response body; a 429 response with a retry-after directive causes the node-run to enter the parked state with the corresponding resume time, and the supervisor wakes the node at that time and re-dispatches it (succeeding when the upstream returns 200 on retry); a 4xx response carrying the configured error-class JSON field surfaces a typed HTTP-family terminal error class; a 4xx with no such field surfaces the stable unspecified-class leaf.

## Falsifier

A 429 response errors a node-run instead of parking, OR the corresponding resume time isn't honored by the supervisor, OR the configured error-class JSON field is ignored.

## Proof

Executable proof.
