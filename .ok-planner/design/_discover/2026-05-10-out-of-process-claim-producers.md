---
topic: out-of-process-claim-producers
kind: choice
---

# ClaimProducers are out-of-process gRPC peers; the only in-rimsky impl is the gRPC client

## Description

Rimsky's storage-side abstraction — the producer of locks, scopes, and resource claims — could be either an in-Go interface implemented by concrete `*FilesystemStore` / `*PostgresStore` structs inside the rimsky binary, or an external peer service rimsky processes call over the wire. Rimsky chose the second model.

`ClaimProducer` is a gRPC service defined in `protocols/proto/v1/claim_producer.proto`. The Go interface in `protocols/claimproducer/claimproducer.go:27` mirrors the wire shape:

- `Open(OpenRequest) → OpenResponse{Acquired | Unavailable}`.
- `Commit(CommitRequest) → CommitResponse`.
- `Abandon(AbandonRequest) → AbandonResponse`.
- `Release(ReleaseRequest) → ReleaseResponse`.
- `Capabilities(CapabilitiesRequest) → CapabilitiesResponse{WriteSemanticsEnvelope, …}`.

The **only concrete `ClaimProducer` implementation in the rimsky module** is the gRPC client at `foundation/integration/remote/`. The bundled producers (`stores/filesystem`, `stores/postgres`, `stores/stub`) are standalone binaries with their own `main.go` that serve the gRPC service. Rimsky never imports any concrete store.

The comment at `foundation/locks/interface.go:9-13` makes the rule explicit: "Type assertions to a concrete producer from any rimsky package are forbidden — the ClaimProducer interface is the only contract." This keeps the interface honest: a third-party producer in any language can serve the protocol; the bundled producers happen to be in Go but get no rimsky-side advantage.

`docs/concepts/claim-producer.md` covers the operator surface: each peer is configured in `rimsky.yml` under `claim_producers:` with `endpoint`, optional `protocols: [claim_producer, lifecycle_subscriber]`, required `write_semantics_envelope`. The five-method protocol + Capabilities handshake is wire-shaped, so producers are language-agnostic.

This choice produces the "atomicity is decoupled" property described in `foundation/integration/runner.go:41-51`: the rimsky-side acquisition transaction commits independently of the producer's own internal transaction. An in-process plugin would have shared the same `*sql.Tx`, which the comment notes is exactly what v2 did via `locks.WithTx` and what v3 deliberately removed.

The trade-offs (visible at `claim-producer.md` "Consumer-visible guarantees" + CLAUDE.md "What this repo is"):

- Producer state (filesystem stagings, postgres items-table flips) is opaque to rimsky and recovered by the producer's own TTL/sweep — see the lock-state-ownership rule at `foundation/locks/interface.go:46-52`.
- Wire-format-compatible producers can be written in any language.
- Every claim acquisition pays at least one extra round trip (rimsky → producer `Open`), but acquires "language-agnostic plug-in" and "single-writer-per-scope enforced by rimsky alone" in return.
- Type-asserting to a concrete producer in the rimsky source tree is a depguard-or-review-time violation.

`docs/concepts/claim-producer.md` "A note on naming" notes the **claim producer** (protocol-level) vs **store** (colloquial bundled-services term) split. The Go interface is `ClaimProducer`; the bundled binaries live under `stores/`. CLAUDE.md "What this repo is" calls "stores" the "bundled-services-layer colloquial term."

## Code surface

- `protocols/proto/v1/claim_producer.proto` — wire definition.
- `protocols/claimproducer/claimproducer.go` — Go interface mirror.
- `foundation/integration/remote/` — gRPC client (only concrete impl in rimsky).
- `foundation/locks/interface.go:9-13` — type-assertion forbidden annotation.
- `stores/filesystem/`, `stores/postgres/`, `stores/stub/` — three reference binaries.
- `cmd/rimsky-claim-producer-conformance/` — conformance binary.

## Prose surface

- `docs/concepts/claim-producer.md` — concept doc + naming note.
- `docs/protocols/claim-producer.md` — implementer's guide.
- `CLAUDE.md` "What this repo is" — out-of-process declaration.
- `CLAUDE.md` "Non-obvious gotchas" — "ClaimProducers are out-of-process."

## Adjacent topics

- `2026-05-10-atomic-acquisition-decoupled-tx` — direct consequence.
- `2026-05-10-lock-state-in-rimsky-not-producer` — what rimsky keeps vs producer keeps.
- `2026-05-10-byte-equal-scope-conflict` — the only conflict mechanism left.
- `2026-05-10-conformance-test-binaries` — how third parties validate against the protocol.
- `2026-05-10-write-semantics-envelope-handshake` — Capabilities() startup handshake.

## Observations

- The "Store" name survives in casual prose and in the directory `stores/`. CLAUDE.md, the docs, and the Go interface all use `ClaimProducer` as the protocol-level name. A vocabulary-lint catches the legacy `Store` usage in protocol-level prose (`docs/concepts/claim-producer.md` includes `<!-- vocabulary-lint-ignore: Store -->` for a single historical reference).
- "Type assertions to a concrete producer from any rimsky package are forbidden" is asserted but not lint-enforced. A grep for `*FilesystemStore` or `*PostgresStore` across rimsky's source should return zero hits.
- The bundled producers are Go binaries because Go was the project's primary language; nothing in the protocol requires Go. The TypeScript executor (`executors/claude-agent/`) is in fact a different-language reference impl of a different protocol, but the producer-language story stays Go for now.
- The five-verb interface (Open/Commit/Abandon/Release/Capabilities) plus the holding-subgraph auto-terminal puts the producer in a single round-trip-per-event posture. A future producer that wanted batched verbs would need a protocol extension; none is on the path.
