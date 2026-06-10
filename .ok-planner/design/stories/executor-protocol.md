---
story: executor-protocol
status: as-is
---

# Service author writes custom executor

## Role

As a service author writing a custom executor, I can implement the gRPC `Execute` server-streaming RPC plus the executor-observability handshake (capabilities, declared error classes, attribute-schema advertising), and have rimsky discover my executor at startup, validate template attributes against my advertised schema, dispatch nodes to my server, accept my emitted events and terminal outcomes, and route errors I raise according to my advertised error classes, so that a custom executor plugs into a rimsky stack without rimsky-internal knowledge.

## Capability

Public executor protocol surface (gRPC `Execute` + observability handshake) that any service author implements; rimsky drives discovery, schema validation, dispatch, event acceptance, terminal resolution, and error-class-aware routing against it.

## Business value

A custom executor plugs into a rimsky stack without rimsky-internal knowledge; the executor's advertised contract (schema, error classes) is honored end-to-end.

## Acceptance

A custom executor implementing the public protocol, registered with rimsky's executor catalog, can be referenced from a template; on instance dispatch, the executor receives a real `Execute` stream with resolved attributes, can emit heartbeats and named events that show up on the rimsky event log, and can resolve to success / error / park / async-callback through the real supervisor terminal-resolution path. Errors with advertised classes route per the template's error-policy.

## Falsifier

A registered executor advertising a declared error class emits it but the policy router treats it as generic, OR an event the executor emits doesn't appear on the event log, OR attributes resolved against the executor's schema bypass the schema validation.

## Proof

Example — the examples module's executor reference extended with a worked walkthrough that boots a running rimsky and exhibits each protocol surface end-to-end.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
