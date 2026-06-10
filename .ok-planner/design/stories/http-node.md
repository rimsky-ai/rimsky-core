---
story: http-node
status: as-is
---

# Template author integrates HTTP upstreams

## Role

As a template author wiring a node against an upstream HTTP API, I can use the bundled `http-node` executor to issue requests, route the response into the node's output attributes, opt into rate-limit parking on 429 (auto-resume from `Retry-After`), and configure which JSON field on an upstream error body carries the error class with a stable fallback when absent, so that I integrate with HTTP upstreams without writing a custom executor.

## Capability

Bundled `http-node` executor: HTTP request dispatch with response routing into node output attributes; 429 parking with `Retry-After` honoring; configurable error-class field on 4xx with stable `_unspecified` fallback.

## Business value

Template authors integrate with HTTP upstreams without writing a custom executor; the bundled executor handles rate limits and error classification natively.

## Acceptance

A template using `http-node` against a real upstream: a 200 response populates the node's output attributes from the response body; a 429 response with `Retry-After` causes the node-run to enter `parked` with the corresponding `resume_at`, and the supervisor wakes the node at that time and re-dispatches it (succeeding when the upstream returns 200 on retry); a 4xx response carrying the configured error-class JSON field surfaces a typed `http/<class>` terminal error; a 4xx with no such field surfaces the stable `_unspecified` leaf.

## Falsifier

429 errors a node-run instead of parking, OR the `resume_at` isn't honored by the supervisor, OR the configured error-class JSON field is ignored.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
