---
topic: byte-equal-scope-conflict
kind: invariant
---

# Scope conflict is byte-equal comparison; producers are responsible for canonicalization

## Description

Two claims targeting "the same data" must conflict at the rimsky layer regardless of how each producer addresses that data. Rimsky has no producer-specific code in the conflict predicate — the comparison is plain `bytes.Equal` on the producer-supplied scope byte slices.

`foundation/locks/conflict.go:64-77` defines `ScopesByteEqual`:

```go
func ScopesByteEqual(a, b []byte) bool {
    if len(a) == 0 || len(b) == 0 { return false }
    return bytes.Equal(a, b)
}
```

Empty slices never conflict (line 65-67) — a `NamedLockSpec` row in a scope-keyed scan can't false-positive against a real scope claim. The only call site is `evaluateScopeConflict` at `foundation/integration/runner_acquire.go:683-700`: it lists existing scope holders by store name, then for every existing holder runs `ScopesByteEqual(candidate.Scope, existing.ScopeData) && !ModeCoexists(...)`. `ModeCoexists` (`conflict.go:44-62`) is the `(write_semantics, intent)` coexistence matrix.

The producer's responsibility is dual: (a) parse the selector from its own DSL (paths, table refs, queue keys, S3 manifests, …) and (b) emit canonical scope bytes such that two acquisitions that should conflict produce byte-equal output. The proto field at `protocols/proto/v1/claim_producer.proto:118-130` declares `scope bytes` and notes it is opaque to rimsky. `docs/concepts/scope.md` puts this in operator language: "the producer knows whether `/foo/bar/` and `/foo/bar` should conflict, whether `analytics_production` and `analytics_PRODUCTION` should be normalized to the same scope, whether trailing-slash matters. Rimsky is opinion-free; it just compares bytes."

A historical alternative is documented inline at `conflict.go:14-18`: "v2's per-store RegionsConflict / UnmarshalRegion methods are gone; canonicalization is the store's responsibility, comparison is rimsky's." The v2 approach would have required rimsky to dispatch to per-producer Go code during the acquisition tx, which is incompatible with the v3 out-of-process model (`2026-05-10-out-of-process-claim-producers`) and with depguard's `pgx-isolation` rule that keeps rimsky's tx separated from producer code.

The byte-equal-scope **uniformity invariant** (`docs/concepts/scope.md`, also cited in CLAUDE.md) is the producer-side contract: across the lifetime of a producer, two `Open` calls that return byte-equal scope MUST also return the same `realized_write_semantics`. Without uniformity, two byte-equal-scope claims could realize different semantics and the conflict matrix would be undefined. This is the producer's invariant to maintain; rimsky does not validate it but relies on it for the matrix predicate at acquire time.

The "concrete-paths only" rule for the standard filesystem store (CLAUDE.md "What this repo is") is exactly this contract applied: the filesystem store canonicalizes its scopes by requiring absolute concrete paths, which guarantees byte-equality whenever two claims target the same path.

## Code surface

- `foundation/locks/conflict.go` — entire file (~80 lines).
- `foundation/integration/runner_acquire.go:683-700` — only call site.
- `protocols/proto/v1/claim_producer.proto:118-145` — `Acquired.scope`, `realized_write_semantics`.
- `stores/filesystem/main.go` — reference impl that canonicalizes by absolute path.
- `stores/postgres/main.go` — reference impl that canonicalizes by region + items-table key.

## Prose surface

- `docs/concepts/scope.md` — the canonical concept document; explicit on producer responsibility and uniformity.
- `docs/concepts/claim.md` — context for selector vs scope.
- `docs/concepts/write-semantics.md` — byte-equal-scope uniformity.
- `CLAUDE.md` "What this repo is" — concrete-paths-only filesystem rule.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — foundation-side conflict predicate.
- `.ok-planner/specs/2026-05-04-service-protocol-contract.md` §2.5 — uniformity invariant declared.

## Adjacent topics

- `2026-05-10-out-of-process-claim-producers` — the architecture that requires byte-equal comparison.
- `2026-05-10-atomic-acquisition-decoupled-tx` — conflict predicate runs inside rimsky's tx.
- `2026-05-10-write-semantics-envelope-handshake` — co-conflicts with byte-equal scope.
- `2026-05-10-named-and-scope-locks-deterministic-order` — scope locks acquired in deterministic order.

## Observations

- `ScopesByteEqual` rejects empty slices on both sides; the documented reason is "a NamedLockSpec row in a scope-keyed scan can't false-positive." This is a defensive guard whose value depends on `evaluateScopeConflict` actually being called with empty-scope inputs — `runner_acquire.go:683` does the scan only for `ClaimSpec` entries (which carry non-empty scope), so the guard is mostly belt-and-suspenders.
- Uniformity is "the producer's invariant to maintain" but is not verified by rimsky. A producer that returns different `realized_write_semantics` for byte-equal scope under different conditions would silently produce wrong conflict-matrix decisions; the conformance suite (`cmd/rimsky-claim-producer-conformance`) should be checked for whether it exercises this.
- The `region` term is a deprecated synonym for `scope`; `docs/concepts/scope.md` lists `[region]` under `deprecated_terms`. The `conflict.go:14-18` comment still cites "v2's per-store RegionsConflict" by historical name.
- Canonicalization is asymmetric: `stores/filesystem/` rejects relative paths client-side (per CLAUDE.md "the standard filesystem store is concrete-paths only"); `stores/postgres/` canonicalizes regions store-internally. The two impls draw the canonicalization line at different layers without rimsky-side visibility.
