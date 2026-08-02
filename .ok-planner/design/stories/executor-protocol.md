---
story: executor-protocol
---

# Service author writes custom executor

## Story

As a service author writing a custom executor, I can implement the `concept:executor` protocol — the unary execute verb plus the executor-observability handshake (capabilities, declared error classes, declared tags, attribute-schema advertising) — and have rimsky discover my executor at startup, validate template attributes against my advertised schema, dispatch nodes to my server, accept my settling terminal outcomes, and route errors I raise according to my advertised error classes, so that a custom executor plugs into a rimsky stack without rimsky-internal knowledge.
