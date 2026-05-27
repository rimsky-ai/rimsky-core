---
topic: three-go-module-split
kind: boundary
---

# Three Go modules tied by `go.work`: `foundation/`, `protocols/`, root

## Description

Rimsky bundles three runtime processes (scheduler, supervisor, control-api), an out-of-process gRPC protocol surface, a modeling layer, and several reference peer-service binaries. All of it could live in a single Go module. Rimsky chose three.

`go.work` ties three Go modules into one workspace:

- **`foundation/`** — module `github.com/rimsky-ai/rimsky-core/foundation`. Cascade engine + claim/lock primitives + integration runner + persistence drivers. Direct deps: `protocols`, `uuid`, `pgx`. Stdlib + minimal third-party.
- **`protocols/`** — module `github.com/rimsky-ai/rimsky-core/protocols`. The three service-protocol Go interfaces + protobuf bindings. Direct deps: `grpc`, `protobuf`, `uuid`. Tightest budget; stdlib + grpc only.
- **Root** — module `github.com/rimsky-ai/rimsky-core`. Modeling layer, cmd binaries, bundled stores, bundled executors. Pulls in heavier libraries (jsonschema, robfig/cron, jcs, testcontainers).

The dependency budgets are visible at `foundation/go.mod`, `protocols/go.mod`, and `go.mod` respectively. An external author depending on `protocols/claimproducer` does not transitively pull jsonschema, robfig/cron, testcontainers — because those live at root.

CLAUDE.md "Package import rules" lays out the boundaries: foundation depends on protocols + stdlib + minimal third-party; protocols on stdlib + grpc + protobuf only; root on everything. The depguard `foundation-internal-isolation` rule (`.golangci.yml:35`) is the lint counterpart of the module boundary — only `foundation/` code may import `foundation/internal/`.

`foundation/locks/interface.go:6-25` and `foundation/locks/types.go:18-31` document the canonical-interface-lives-in-`protocols` rule and the rimsky-side alias pattern: `ClaimProducer`'s wire shape is in `protocols/proto/v1/claim_producer.proto`; the Go interface mirrors it in `protocols/claimproducer/claimproducer.go`; `foundation/locks/` aliases `ClaimSpec` and `ClaimResult` into the foundation package for caller convenience while keeping the canonical type in protocols.

The three-module split has three observable consequences:

1. **External implementers of `ClaimProducer`, `Executor`, or `LifecycleSubscriber` depend on a small interface-only module.** Their `go.mod` lists protocols and that's it.
2. **The three runtime processes can never import each other** — their packages live under the root module's `cmd/` and `modeling/`, and have no Go-level relationship. Cross-process coordination is via Postgres only.
3. **Adding a fourth interface protocol** means deciding upfront whether it goes in `protocols/` (third-party-implementable), `foundation/` (foundation-private), or root (modeling-only). The decision is structural; once made, the dependency graph follows.

A future plug-in compiled against `protocols/` is binary-compatible with any rimsky build at the matching minor version. Wire-level changes happen in `protocols/proto/v1/*.proto`; binary-compat changes happen via Go SemVer in `protocols/go.mod`.

## Code surface

- `go.work` — workspace declaration.
- `go.mod`, `foundation/go.mod`, `protocols/go.mod` — three module declarations.
- `foundation/locks/interface.go:6-25`, `foundation/locks/types.go:18-31` — alias rationale.
- `.golangci.yml:14-50` — depguard rules (pgx-isolation, foundation-internal-isolation).

## Prose surface

- `CLAUDE.md` "Package import rules (enforced; violations break the build)".
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — foundation as a module.
- `.ok-planner/specs/2026-05-04-modeling-layer-contract.md` — modeling as the root-module layer.
- `.ok-planner/specs/2026-05-04-service-protocol-contract.md` — the three protocols.

## Adjacent topics

- `2026-05-10-depguard-enforced-package-boundaries` — lint enforcement that complements the module boundary.
- `2026-05-10-out-of-process-claim-producers` — protocols module is what makes third-party producers possible.
- `2026-05-10-stdlib-slog-and-minimal-deps` — per-module dependency budgets.

## Observations

- The module split was completed by the "layer crystallization design" (`.ok-planner/specs/2026-05-04-layer-crystallization-design.md`); earlier history (per the `.ok-planner/archive/` references) had a single module that the v3 design split out.
- The `go.work` workspace approach means a Go dev can `cd foundation && go test ./...` and have replace-directives implicit; CI compiles each module independently to verify the dependency budget.
- `foundation/internal/pgtest/` is the canonical example of "foundation-private but allowed under depguard's foundation-internal-isolation rule" — only foundation code may import it. This protects a fixtures package that exposes pgx that modeling/ should not see.
- A future fourth protocol would also force a decision about whether it ships its own Go module or joins `protocols/`. The current pattern is "one module for all interfaces"; a fourth surface that needed independent versioning could break this.
