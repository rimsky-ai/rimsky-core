---
concept: claim-producer
definition: |
  The protocol-level term for a service that produces claim handles for Rimsky's lock-and-claim primitives. Implements five methods (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`). Out-of-process; rimsky talks to claim producers over gRPC.
proto_symbol: ClaimProducer in protocols/proto/v1/claim_producer.proto
config_field: rimsky.yml:claim_producers
api_surface: (none)
related: [claim, claim-handle, scope, write-semantics]
deprecated_terms: [Store, StoreService, Bridge]
---

# Claim producer

## Definition

The protocol-level term for a service that produces claim handles for Rimsky's lock-and-claim primitives. Implements five methods (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`). Out-of-process; rimsky talks to claim producers over gRPC.

## Why it exists

Rimsky's coordination primitives (claims, named locks) need to know about state outside the orchestrator: the filesystem holding workflow outputs, the postgres database queue, the S3 bucket of artifacts. But Rimsky cannot know the structure of every possible state — there are too many.

The claim-producer protocol is the contract. A service implements five methods; Rimsky calls them at acquisition (`Open`), at executor terminal (`Commit`/`Abandon`/`Release`), and at startup (`Capabilities`). The producer is the source of truth for the structure of its underlying state; Rimsky is the source of truth for which claims are currently held.

This split keeps the protocol minimal. Producers don't internally persist lock state — they're stateless servers that know how to translate their selectors into scope bytes and how to mutate their underlying state on commit/abandon. Rimsky owns the claim-handle table; producers own everything else.

## The five methods

- **`Open(OpenRequest) → OpenResponse`**: produce a producer-supplied address for the executor and register whatever producer-side state the `(intent × write_semantics)` combination requires. The response is a `oneof` carrying either an `Acquired` message (with `address`, `payload`, `scope`, and `realized_write_semantics`) or an `Unavailable` message (no claim available right now; rimsky retries on the next scheduler tick).
- **`Commit(CommitRequest) → CommitResponse`**: signals that the consumer of the claim succeeded. The producer decides what to do with its own state per its own configuration.
- **`Abandon(AbandonRequest) → AbandonResponse`**: signals that the consumer of the claim failed. The producer decides what to do with its own state per its own configuration.
- **`Release(ReleaseRequest) → ReleaseResponse`**: tear down producer-side read state (snapshot, MVCC transaction) for a read claim. Distinct from `Commit`/`Abandon`, which are write-claim-shaped.
- **`Capabilities(CapabilitiesRequest) → CapabilitiesResponse`**: startup handshake. Returns the producer's `WriteSemanticsEnvelope`. Probed once per protocol per service at process startup.

## A note on naming: "claim producer" vs "store"

In protocol-level prose — wire protocols, conformance suites, the Go interface name — the canonical term is **claim producer**. The Go interface is `ClaimProducer`.

In casual operator parlance and in the reference-implementation tree (`stores/filesystem/`, `stores/postgres/`, `stores/stub/`), the colloquial name **store** survives. "The filesystem store," "the postgres store," "the stub store" are normal ways to talk about data-backed reference impls.

The two names refer to the same thing — a service that implements the five-method `ClaimProducer` protocol. Use "claim producer" in protocol-level discussion (someone implementing the protocol; someone reading the proto sources); "store" is fine in casual contexts about the bundled reference impls.

## How you encounter it

- **Operator config**: the `claim_producers:` block in `rimsky.yml`. Each entry has an `endpoint` (gRPC URL), an optional `protocols: [...]` list, and a required `write_semantics_allowed: [...]`.
- **Implementing a producer**: speak gRPC against `protocols/proto/v1/claim_producer.proto`. The reference impls (filesystem, postgres, stub) live under `stores/` and are runnable as standalone binaries.
- **Conformance**: `cmd/rimsky-claim-producer-conformance` exercises a producer against the wire-protocol contract.

## Consumer-visible guarantees

- The producer never persists lock state — rimsky's record of the claim is the sole authority. Producers that try to mirror the ledger reinvent eligibility-checking and inevitably drift.
- The reader-lease serialization pattern is forbidden for `staged_async`. Honest support requires snapshot delegation or native MVCC pass-through.
- Each verb takes a `claim_id` (rimsky-generated UUID) so producers can correlate state across verbs.

## Common mistakes

- **Rimsky's "store" (the bundled-services colloquialism) ≠ Redux store, Vue store, Svelte store.** A Rimsky bundled-services-layer "store" is a data-backed claim-producer reference impl. Nothing to do with state-management libraries in JavaScript frameworks.
- Citing `Store` (the legacy protocol-level Go interface name) as if it still existed. <!-- vocabulary-lint-ignore: `Store` --> The interface was renamed to `ClaimProducer`; backticked-`Store` references in protocol-level prose are stale.
- Implementing a producer that internally serializes on lock-shaped predicates as a way to fake `staged_async`. This is forbidden — honest support requires snapshot delegation or MVCC pass-through.

## See also

- [`claim.md`](claim.md)
- [`claim-handle.md`](claim-handle.md)
- [`scope.md`](scope.md)
- [`write-semantics.md`](write-semantics.md)
