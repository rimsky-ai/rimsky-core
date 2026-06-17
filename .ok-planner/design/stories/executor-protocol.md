---
story: executor-protocol
status: as-is
---

# Service author writes custom executor

## Role

As a service author writing a custom executor, I can implement the `concept:executor` protocol — the unary execute verb plus the executor-observability handshake (capabilities, declared error classes, declared tags, attribute-schema advertising) — and have rimsky discover my executor at startup, validate template attributes against my advertised schema, dispatch nodes to my server, accept my settling terminal outcomes, and route errors I raise according to my advertised error classes, so that a custom executor plugs into a rimsky stack without rimsky-internal knowledge.

## Capability

Public executor protocol surface — a unary `Execute(req) → Outcome` verb plus the observability handshake (see `concept:executor`) — that any service author implements; rimsky drives discovery, schema validation, dispatch, terminal resolution, and error-class-aware routing against it.

## Business value

A custom executor plugs into a rimsky stack without rimsky-internal knowledge; the executor's advertised contract (schema, tags, error classes) is honored end-to-end.

## Acceptance

A custom executor implementing the public protocol, registered with rimsky's executor catalog, can be referenced from a template; on instance dispatch, the executor receives a real unary request with resolved attributes, returns a settling terminal directly (Success / Error / Park with `attributes_delta` and `tags`) or defers via AwaitAsyncCallback and POSTs the eventual verdict to the callback URL; errors with advertised classes route per the template's error-policy; tags on the settling terminal are visible to downstream subscribers via the `terminal/*` signal with a CEL `when:` filter on `payload.tags`.

## Falsifier

A registered executor advertising a declared error class emits it but the policy router treats it as generic, OR the unary RPC's response shape is rejected by the supervisor, OR a tag emitted on a settling terminal does not appear in downstream subscription matches, OR an async-callback POST is dropped after the supervisor that registered it restarts.

## Proof

Example — a shipped executor reference paired with a worked walkthrough that boots a running rimsky and exhibits each protocol surface end-to-end (unary execute, async-callback registration + delivery, error-class routing, tag-based subscription).
