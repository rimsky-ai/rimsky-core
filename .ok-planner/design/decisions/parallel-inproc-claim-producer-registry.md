---
decision: parallel-inproc-claim-producer-registry
status: as-is
aliases: []
---

# Claim producers get an in-process registry parallel to the executor one

## Choice

The runtime layer holds an in-process registry for the claim-producer protocol, parallel to the existing executor in-process registry. Registration binds a producer name to a handler implementing the protocol's Go interface, the producer's capabilities as construction data, and explicit advertisement of the optional mix-ins it implements (validation, data-processing). The registry hands out in-process clients that satisfy the same consumer-facing interfaces the gRPC peer clients satisfy, so the dispatch paths (claim acquisition, validation pipeline, data-processing) are mode-blind. The in-process client enforces the same capability envelope the gRPC client enforces: a realized write semantics outside the advertised envelope is an error, split-scope and scopes-conflict calls are gated on the advertised capability flags (with the same byte-equality conflict fallback), and registration rejects an empty write-semantics envelope. Mix-in advertisement must be consistent: a registration advertising a mix-in protocol must supply the mix-in client, and vice versa.

## Rationale

Executors already have an in-proc registry with a single-verb surface; claim producers need the parallel piece but must express the fuller multi-verb + mix-in shape to match the gRPC contract semantically. Enforcing the capability envelope in the in-proc client keeps the two modes protocol-equivalent: a template dispatching to a producer behaves the same whether the producer is in-process or remote.

## Alternatives

- Loopback gRPC (in-memory conn or Unix socket) — rejected: reintroduces the wire-serialization overhead in-process dispatch exists to eliminate.
- Core-verbs-only registry (drop mix-ins) — rejected: creates a capability asymmetry between modes; a template using a mix-in would work remotely but not in-process.
- Envelope enforcement only at the gRPC surface — rejected: an in-proc handler bug would surface differently across modes, breaking protocol equivalence.
