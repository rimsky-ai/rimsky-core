---
concept: write-semantics
definition: |
  The per-claim verdict from `ClaimProducer.Open` describing how writes against the claimed scope are visible to other claims. One of `sync`, `staged_async`, `blocking_async`, `read_only`. The conflict matrix is parameterized over write-semantics so different producers can offer different concurrency models.
proto_symbol: WriteSemantics in protocols/proto/v1/claim_producer.proto
config_field: rimsky.yml:claim_producers
api_surface: (none)
related: [claim, claim-producer, claim-handle, scope]
deprecated_terms: []
---

# Write semantics

## Definition

The per-claim verdict from `ClaimProducer.Open` describing how writes against the claimed scope are visible to other claims. One of `sync`, `staged_async`, `blocking_async`, `read_only`. The conflict matrix is parameterized over write-semantics so different producers can offer different concurrency models.

## Why it exists

A filesystem store and a postgres store have different concurrency capabilities. The filesystem can't isolate concurrent writers; postgres can. A naive design would either pick the lowest-common-denominator (filesystem-style: serialize everything) or the highest (postgres-style: assume MVCC works everywhere). Rimsky picks neither — it lets each producer declare what it offers.

The four values:

- **`sync`** — synchronous in-place writes; no staging. Two writers on byte-equal scope conflict.
- **`staged_async`** — writes go to a producer-internal staging area; reads can dispatch concurrently with writes on the same scope. Two writers still conflict; reads and writers can coexist.
- **`blocking_async`** — staging area; reads block until commit. Two writers conflict; reads and writers also serialize.
- **`read_only`** — read-only access (no writes possible). Two readers coexist trivially.

The producer's `Capabilities()` startup handshake returns the **write-semantics envelope** — the set of values it may return on `Open`. The operator declares a subset envelope per service in `rimsky.yml`; the capability handshake validates operator-declared ⊆ producer-declared.

## Per-claim vs. per-producer

The producer-level envelope is the *set* of values a producer may return; the per-claim **realized write semantics** is the value the producer actually returned for a specific `Open` call. Realized values are persisted on the claim-handle row and used as the conflict-predicate input.

Producers that support multiple values choose per-claim based on the claim's intent and selector. A producer that always returns one value advertises a single-element envelope.

## How you encounter it

- **Wire**: `OpenResponse.acquired.realized_write_semantics` carries the per-claim verdict.
- **Operator config**: `claim_producers.<name>.write_semantics_allowed:` declares the operator's allowed subset (must be ⊆ producer-advertised).
- **Capability handshake failure**: if the operator's declared envelope is not a subset of the producer's advertised envelope, the supervisor refuses to start.

## Consumer-visible guarantees

- Across the lifetime of a producer, two `Open` calls returning byte-equal scope MUST return the same realized write-semantics value (the byte-equal-scope uniformity property).
- The conflict matrix never causes a phantom acquisition: if rimsky proceeds with a claim, its declared semantics are compatible with all currently-held conflicting claims; if rimsky rejects acquisition, the operator can resolve by retrying when the conflict clears.
- The reader-lease serialization pattern is forbidden for `staged_async`. Honest support requires snapshot delegation or native MVCC pass-through; producers cannot fake `staged_async` by serializing internally.

## Common mistakes

- Confusing the producer's envelope with the per-claim realized value. Envelope is the set of possible values; realized is the value for one specific claim.
- Declaring a `write_semantics_allowed` for a service that exceeds the producer's advertised envelope. The capability handshake will fail at startup.
- Expecting `staged_async` to give you producer-internal serialization for free. Producers must offer real concurrent-read access to deliver `staged_async` honestly.

## See also

- [`claim.md`](claim.md)
- [`claim-producer.md`](claim-producer.md)
- [`claim-handle.md`](claim-handle.md)
- [`scope.md`](scope.md)
