---
topic: write-semantics-envelope-handshake
kind: invariant
---

# `WriteSemantics` is per-claim; producer advertises an envelope at `Capabilities()`; operator narrows it; `Open` realizes one value

## Description

A claim's **write semantics** — `sync`, `staged_async`, `blocking_async`, `read_only` — determine how the conflict matrix (`ModeCoexists`) treats concurrent claims on byte-equal scope. A producer might support multiple semantics depending on the kind of resource (`docs/concepts/write-semantics.md`: a postgres-MVCC-backed producer might offer `sync` for one resource and `staged_async` for another). Pinning the choice as a per-binary capability is too coarse; pinning per claim is too fine without an upper bound.

Rimsky uses a three-level structure:

1. **Producer advertises an envelope (a SET) via `Capabilities()`** at startup (`protocols/proto/v1/claim_producer.proto:85-87`). E.g. `{SYNC, STAGED_ASYNC}`.
2. **Operator declares a narrowing envelope per producer** in `rimsky.yml` (`claim_producers[*].write_semantics_envelope`). Rimsky validates strict subset at startup (`foundation/locks/types.go:154-161`): the operator-declared envelope must be ⊆ the producer's advertised envelope.
3. **Each `Open` returns the realized value for that specific claim** (`Acquired.realized_write_semantics`, `claim_producer.proto:125-130`). The realized value MUST be a member of the producer's advertised envelope AND MUST be uniform across byte-equal-scope claims (the **uniformity invariant** per `.ok-planner/specs/2026-05-04-service-protocol-contract.md` §2.5).

`UNKNOWN` is the proto-default zero value (`claim_producer.proto:71`); producers must not return it. The supervisor rejects claim results with `UNKNOWN`.

The rationale at `foundation/locks/types.go:114-116`:

> Per Phase 4 of the layer-crystallization design (2026-05-04) the previously single-valued capability is replaced by an envelope: a ClaimProducer advertises a SET of permissible values via Capabilities, and Open returns the realized value per claim.

The **uniformity invariant** lets the conflict-check at acquire time use the **holder's** recorded semantics for both sides of the matrix (`foundation/integration/runner_acquire.go:691-697`). Without uniformity, two byte-equal-scope claims could realize different semantics and the matrix would be undefined.

The legacy single-value `write_semantics:` (vs `write_semantics_envelope:`) YAML key is accepted as a single-element envelope shortcut (`2026-05-10-unified-rimsky-yml-config`). Pre-v1 transition affordance.

The interaction with `@blessed-invariant 9b` (producers must not internally serialize on lock-shaped predicates): a producer that advertises `STAGED_ASYNC` must implement snapshot delegation or MVCC pass-through — reader-lease serialization is forbidden. The honest support requirement is part of the contract; the conformance binary checks it.

Operator config is a deployment-level vault: a producer that advertises `STAGED_ASYNC` can be restricted to `SYNC`-only in a deployment that hasn't validated the producer's MVCC behavior. The envelope narrowing is one-way (subset only); the validation fails fast at startup if the operator declares values outside the producer's set.

A future deployment can add another semantic if the matrix is extended; the strict-subset rule keeps backward-compat. The matrix itself (`ModeCoexists` at `foundation/locks/conflict.go:44-62`) is the place where new semantics would need to declare their compatibility with existing ones.

## Code surface

- `protocols/proto/v1/claim_producer.proto:70-130` — `WriteSemantics` enum, `Capabilities`, `Acquired.realized_write_semantics`.
- `foundation/locks/types.go:114-161` — envelope + validation.
- `foundation/locks/conflict.go:44-62` — `ModeCoexists` matrix.
- `foundation/integration/runner_acquire.go:691-697` — conflict-check uses holder's recorded semantics.
- `foundation/persistence/postgres/migrations/001-initial.sql:170-209` — `rimsky_claim_handle.realized_write_semantics` column.

## Prose surface

- `docs/concepts/write-semantics.md` — concept-doc treatment.
- `CLAUDE.md` "Non-obvious gotchas" — envelope + legacy alias.
- `.ok-planner/specs/2026-05-04-service-protocol-contract.md` §2.5 — uniformity invariant.

## Adjacent topics

- `2026-05-10-byte-equal-scope-conflict` — `ScopesByteEqual` is the conflict gate.
- `2026-05-10-lock-state-in-rimsky-not-producer` — invariant 9b's reader-lease ban.
- `2026-05-10-unified-rimsky-yml-config` — `write_semantics_envelope` in YAML.
- `2026-05-10-conformance-test-binaries` — conformance probes uniformity.
- `2026-05-10-out-of-process-claim-producers` — `Capabilities()` is the startup RPC.

## Observations

- The uniformity invariant is producer-side (per the contract spec); rimsky relies on it but does not verify it across calls. A producer that returns different `realized_write_semantics` for byte-equal scope under different conditions would silently produce wrong conflict-matrix decisions. The conformance binary should check this; CLAUDE.md doesn't enumerate which conformance assertions cover it.
- `UNKNOWN` as proto default reflects standard protobuf practice: a zero-valued field accidentally sent is rejected explicitly rather than silently treated as `SYNC`. This is invariant-adjacent: a future client implementation that fails to set the field gets caught at the supervisor's acceptance check.
- The legacy single-value `write_semantics:` alias is operator-facing; CLAUDE.md "Non-obvious gotchas" cites it but the code path is in `modeling/config/`. A YAML linter would catch the mixed usage but isn't shipped.
- The matrix is a 4x4 cross-product with intent (`r`/`rw`) folded in; it's tabulated in `foundation/locks/conflict.go::ModeCoexists`. New semantics that don't fit the existing axes (e.g. a third intent) would require extending the matrix structurally.
