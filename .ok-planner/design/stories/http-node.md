---
story: http-node
status: as-is
---

# Template author integrates HTTP upstreams

## Story

As a template author wiring a node against an upstream HTTP API, I can use the bundled HTTP-node executor to issue requests, route the response into the node's output attributes, opt into rate-limit parking on the 429 status code (auto-resume from the upstream's retry-after directive), and configure which JSON field on an upstream error body carries the error class with a stable fallback when absent, so that I integrate with HTTP upstreams without writing a custom executor.

Bundled HTTP-node executor: HTTP request dispatch with response routing into node output attributes; rate-limit parking on the 429 status code honoring the upstream's retry-after directive; configurable error-class field on 4xx with a stable unspecified-class fallback.

Template authors integrate with HTTP upstreams without writing a custom executor; the bundled executor handles rate limits and error classification natively.
