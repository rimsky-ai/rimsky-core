# Implementing a claim producer

This guide is for developers implementing a claim producer — in any language — and wiring it into a Rimsky deployment. The wire contract lives at `protocols/proto/v1/claim_producer.proto`; this guide is the practical companion.

<!-- @source: ../../.ok-planner/design/concepts/claim-producer.md -->
> The protocol-level term for a service that produces claim handles for Rimsky's lock-and-claim primitives. Implements five methods (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`). Out-of-process; rimsky talks to claim producers over gRPC.

A note on terminology: the protocol-level term is **claim producer** (the service implementing `ClaimProducer` over gRPC). The colloquial term **store** survives at the bundled-services layer for data-backed reference impls (filesystem store, postgres store, stub store).

> **Auth-blind advisory.** Rimsky has no machinery for credentials, encryption, or access control. Encrypt sensitive bytes before handing them to Rimsky if you need protection. Service-to-service auth is operator-configured at the deployment layer (mTLS, IAM).

---

## 1. The wire contract

The producer is a service. Rimsky's processes dial it at startup, run a `Capabilities()` handshake, and issue four runtime verbs over gRPC:

```protobuf
service ClaimProducer {
  rpc Capabilities(CapabilitiesRequest) returns (CapabilitiesResponse);
  rpc Open(OpenRequest)                 returns (OpenResponse);
  rpc Commit(CommitRequest)             returns (CommitResponse);
  rpc Abandon(AbandonRequest)           returns (AbandonResponse);
  rpc Release(ReleaseRequest)           returns (ReleaseResponse);
}
```

`OpenResponse` is a `oneof` carrying either an `Acquired` message (with `address`, `payload`, `scope`, and `realized_write_semantics`) or an `Unavailable` message (no claim available right now; rimsky retries on the next scheduler tick). Each terminal verb has its own request/response pair so the producer can tag idempotency keys and structured per-verb fields without overloading a shared envelope.

Source: `protocols/proto/v1/claim_producer.proto`.

That's the entire runtime contract. Rimsky owns the lock-state bookkeeping, the orphan reaper, the state machine, and verb dispatch. You own the underlying state (filesystem paths, postgres rows, in-memory map) and the per-verb semantics.

## 2. Five obligations every implementation must meet

1. **Run your own TTL / sweep for orphan reclamation.** Rimsky-side reaping handles its own claim-handle records. The producer-side state lives outside Rimsky's view; the producer must run its own TTL sweep so partial commits don't leak. Recommended TTL: strictly greater than `5 × heartbeat_interval` (Rimsky's orphan-reap window) so a healthy producer doesn't strip a claim out from under a live supervisor.
2. **Record `claim_id` before any state mutation in `Open`.** `Open` is invoked inside Rimsky's atomic acquisition transaction. If the producer mutates state but Rimsky's transaction rolls back, the producer is left with orphan state. Recording `claim_id` first lets the producer's TTL sweep identify and clean up orphans.
3. **All terminal verbs must be idempotent in `claim_id`.** `Commit(claim_id)`, `Abandon(claim_id)`, `Release(claim_id)` must be safe to retry. Rimsky may retry on transient gRPC failures.
4. **Do not depend on Rimsky calling `Abandon` for orphan cleanup.** When Rimsky's orphan reaper deletes a stale claim-handle row, it does not fire any producer verb. The producer's own TTL handles that case.
5. **Canonicalize `scope` bytes.** Two `Open` calls that should conflict must produce byte-equal `scope` values. Rimsky's foundation compares byte-for-byte; no glob, no range-match, no canonicalization on the Rimsky side.

## 3. Byte-equal-scope uniformity

Across the lifetime of the producer process, two `Open` calls returning byte-equal `scope` MUST return the same `realized_write_semantics`. Rimsky relies on this for the conflict predicate; producers enforce.

In practice: if your producer supports both `staged_async` and `sync` modes, it must consistently return the same one for any given canonical scope. The simplest path is "one mode per producer process" (single-element envelope); supporting per-claim variation requires per-scope state.

## 4. Per-verb semantics

### `Open(OpenRequest) → OpenResponse`

<!-- @source: ../../.ok-planner/design/concepts/claim.md -->
> A node-declared assertion that the node will read or read-write a producer-defined slice of state for the duration of its run. Claims are acquired before the node's executor runs and resolved at terminal. Each claim binds an alias, an intent (`r` or `rw`), a producer name, and a selector.

Inside `OpenRequest`:

- `claim_id` — Rimsky-generated UUID; record it first (obligation 2).
<!-- vocabulary-lint-ignore: template_id -->
- `producer_name`, `selector`, `intent`, `alias`, `template_id`, `instance_id` — the resolved claim spec. `selector` is post-substitution; the producer parses it. The proto field is named `template_id` (the wire-protocol field name) and carries the content hash; `instance_id` is the instance UUID. Both are opaque to rimsky and provided for namespace routing or trace correlation.

Return one of two `oneof` variants in `OpenResponse`:

- **`Acquired`** — the claim was acquired. Fields:
    - `address` — producer-supplied bytes the executor uses to access claimed state.
    - `payload` — producer-supplied data captured at acquisition (e.g. a picked queue item's user data).
    - `scope` — canonicalized scope bytes. Two acquisitions that should conflict must produce byte-equal `scope`.
    - `realized_write_semantics` — the per-claim value. Must be a member of the envelope returned by `Capabilities`.
- **`Unavailable`** — no claim available right now (e.g. an empty items-table queue). No fields. Rimsky retries on the next scheduler tick. Producer-side faults flow as gRPC error status codes, not as an `Unavailable` response.

Resume detection is the producer's responsibility. If `Open` arrives with a `claim_id` the producer has seen before, the producer recognizes the resume internally (lookup by `claim_id` against its own state). There is no `resumed` flag on `Open`.

### `Commit(CommitRequest) → CommitResponse`

`CommitRequest` carries `claim_id`, `scope`, `address`. Signals that the consumer of the claim succeeded. Producer disposition is per-producer config. Examples:

- For `rw` claims on `staged_*` mode: atomically publish the staging area's contents into live state.
- For `sync`-mode `rw` claims: producer-side no-op (writes already live).
- For pick-policy claims: apply the configured commit-default action.

Idempotent in `claim_id` (obligation 3). `CommitResponse` has no fields.

### `Abandon(AbandonRequest) → AbandonResponse`

`AbandonRequest` carries `claim_id`, `scope`, `address` (where `address` may be empty — the producer identifies its own state by `claim_id`). Signals that the consumer of the claim failed. Producer disposition is per-producer config. Examples:

- For `staged_*` `rw` claims: discard the staging area without publishing.
- For pick-policy claims: apply the configured abandon-default action.
- For `sync` `rw` claims: degenerate — direct writes cannot be undone. Document this as an honest limitation in your producer's README.

Not called for read-only claims (Rimsky calls `Release` instead). Idempotent in `claim_id`. `AbandonResponse` has no fields.

### `Release(ReleaseRequest) → ReleaseResponse`

`ReleaseRequest` carries `claim_id`, `scope`, `address`. Tear down producer-side read state (snapshot, MVCC transaction) for a read claim. Fires only when the producer registered such state (relevant for `staged_async`; not exercised by every reference producer). Idempotent in `claim_id`. `ReleaseResponse` has no fields.

### `Capabilities(CapabilitiesRequest) → CapabilitiesResponse`

`CapabilitiesRequest` has no fields. `CapabilitiesResponse` returns the `WriteSemanticsEnvelope` — the set of `WriteSemantics` values this producer may return from `Open`. Probed once per service at process startup; cached for the process's lifetime.

The operator declares a subset envelope per service in `rimsky.yml` under `write_semantics_allowed:`. The capability handshake validates operator-declared ⊆ producer-declared; a mismatch fails Rimsky startup.

## 5. Verb-firing matrix per claim shape

| Claim shape | `write_semantics` | Verbs invoked at terminal |
|---|---|---|
| Scoped-access `r` | `sync` / `blocking_async` | None — claim-handle deletion is sufficient |
| Scoped-access `r` | `staged_async` | `Release(claim_id)` |
| Scoped-access `rw` | `sync` | `Commit(claim_id)` (no-op) or `Abandon(claim_id)` (degenerate) |
| Scoped-access `rw` | `staged_*` | `Commit(claim_id)` (atomic swap) or `Abandon(claim_id)` |
| Pick-policy claim | (any) | `Commit(claim_id)` or `Abandon(claim_id)` |

For held claims, Rimsky fires exactly one automatic resolution at holding-subgraph completion. Aggregate outcome — all-completed → `Commit`; any-failed → `Abandon` — drives the verb. From the producer's perspective, the call is indistinguishable from a non-held terminal.

## 6. Inertness — what Rimsky won't do with claim content

<!-- @source: ../../.ok-planner/design/concepts/claim-handle.md -->
> The persistent row asserting "holder H has acquired scope S for purpose P." Implementation of an acquired claim. Carries the rimsky-generated `claim_id`, holder identity, scope bytes, producer-returned address and payload, the realized write semantics, and a held flag.

Address, payload, and scope from `OpenResponse.acquired` are opaque to Rimsky. Rimsky reads claim content by named-field path only at substitution-leaf extraction; it never logs, validates, transforms, normalizes, decrypts, hashes, indexes, pattern-matches, attaches to traces, or includes the bytes in errors.

This means: encrypted fields stay encrypted in transit. Operators who want fields out of Rimsky's address space encrypt before passing.

## 7. Atomicity is decoupled

Rimsky opens a transaction, claims the worker-request, INSERTs claim-handle rows, RPCs `ClaimProducer.Open` (the producer runs in its own transaction), UPDATEs claim-handle addresses, INSERTs claim-holders, COMMITs.

Failures on either side are recovered separately:

- Rimsky-side failures are recovered via Rimsky's orphan reaper.
- Producer-side failures are recovered via the producer's own TTL/sweep (obligation 1).

The two transactions are decoupled. A failure on one side does not roll back the other.

## 8. Conformance

The `cmd/rimsky-claim-producer-conformance` binary exercises a producer against the wire-protocol contract. Run it pointing at your producer endpoint to verify `Open`/`Commit`/`Abandon`/`Release`/`Capabilities` behave correctly.

## 9. Reference impls

Three reference producers ship under `stores/`:

- `stores/filesystem/` — concrete-paths only; `sync` write semantics.
- `stores/postgres/` — scoped-access plus items-table queue semantics implemented via pick policies.
- `stores/stub/` — in-memory test fixture.

Each is a standalone Go binary plus a Dockerfile and config example.

## See also

- [`../../.ok-planner/design/concepts/claim-producer.md`](../../.ok-planner/design/concepts/claim-producer.md)
- [`../../.ok-planner/design/concepts/claim.md`](../../.ok-planner/design/concepts/claim.md)
- [`../../.ok-planner/design/concepts/claim-handle.md`](../../.ok-planner/design/concepts/claim-handle.md)
- [`../../.ok-planner/design/concepts/scope.md`](../../.ok-planner/design/concepts/scope.md)
- [`../../.ok-planner/design/concepts/write-semantics.md`](../../.ok-planner/design/concepts/write-semantics.md)
