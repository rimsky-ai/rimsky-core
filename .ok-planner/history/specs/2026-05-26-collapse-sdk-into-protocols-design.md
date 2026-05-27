# Collapse `sdk/go` into `protocols` — one public Go module

**Date:** 2026-05-26
**Status:** spec (approved-for-planning)
**Related sketch:** `.ok-planner/sketches/2026-05-26-collapse-sdk-into-protocols.md`
**Supersedes framing in:** open question #10 of `.ok-planner/sketches/2026-05-14-rimsky-development-kit.md`

## Summary

Make **`protocols` the single public-facing Go module** a service
implementer depends on. Today a Go consumer must wire up two public
modules (`protocols` *and* `sdk/go`); the "SDK" carries a postgres/docker
dependency that has no business in a contract surface; and the
claim-producer contract types leak through an internal module
(`foundation/locks`). This spec folds the genuinely-contract and
genuinely-useful parts of `sdk/go` into `protocols`, evicts the rest,
carves the one postgres/docker coupling into its own opt-in module, and
closes the contract-type leak — leaving a crisp public/internal line.

The driving thesis: `protocols` is the true dependency leaf (everything
depends on it; it depends on no rimsky package). "The SDK" was a satellite
layer *on top of* the contract — backwards from the dependency reality.
The fix is to fold the helper code *into* the contract module, not to have
the contract re-export through a satellite.

## Goals

- One public Go module for service implementers: `protocols`.
- `protocols` stays dependency-lean: stdlib + grpc + protobuf + uuid +
  yaml.v3 only; no DB driver, no test infrastructure.
- The claim-producer contract types have exactly one canonical Go home
  (`protocols/claimproducer`); no internal module re-exports them.
- The single postgres/docker coupling (`testpg`) becomes an opt-in module
  carrying its own deps, so neither `protocols` nor any consumer that
  doesn't want a Postgres test container inherits testcontainers/pgx.
- The contract/convenience and public/internal lines are legible in the
  import path and enforced by lint.
- The whole repo builds, tests, lints, and passes conformance after the
  move; the licensing boundary stays clean.

## Non-goals

- **`internal/`-fencing of `foundation`.** Deferred. Closing the `locks`
  leak removes the *reason* anyone reaches into `foundation`; pushing the
  now-private foundation packages under `internal/` is a separate,
  sequencing-sensitive breaking change to be done after external consumers
  have migrated. See "Follow-ups."
- **Downstream external-consumer migration.** Repointing any external Go
  consumer onto the new layout is its own-repo task, gated on this spec
  landing. Out of scope here; noted under "Follow-ups."
- **Renaming `foundation`.** It keeps its name and location.
- **Building any part of the future RDK.** This spec only records the
  framing that there is no separate Go SDK; the RDK is a different-purpose,
  Python-first authoring layer, not a Go SDK successor.

## Current state (grounded)

`go.work` ties four modules: `.` (root), `./foundation`, `./protocols`,
`./sdk/go`. Verified dependency DAG: `protocols` has no rimsky deps;
`foundation → protocols`; `sdk/go → protocols`; root → all three.

`protocols/go.mod` requires only `google.golang.org/grpc` and
`google.golang.org/protobuf` (plus their indirects). It already contains:

- `protocols/proto/v1/gen/` — generated protobuf bindings (the wire
  contract).
- `protocols/claimproducer/` and `protocols/lifecycle/` — hand-written Go
  contract ergonomics.

`sdk/go` contains six top-level packages:

| Package | Nature | In-repo import sites | Destination |
|---|---|---|---|
| `sdk/go/conformance/{executor,claimproducer,publisher,validation,dataprocessing,blobbackend}` | executable contract spec | 7 conformance cmd binaries | `protocols/conformance/...` |
| `sdk/go/stores/action` | claim-producer pick-policy action vocabulary | **13** | `protocols/action` |
| `sdk/go/server` | server-side implementer scaffolding (HTTP+JSON bridge, lifecycle, observability) | 2 | `protocols/serverkit` |
| `sdk/go/publisher` | publisher-side message-emit retry/backoff helper | 0 | `protocols/publisherkit` |
| `sdk/go/ops` | generic DSN/slog/health glue | 1 | `internal/ops` (core-internal, not public) |
| `sdk/go/testpg` | plain-Postgres testcontainer helper | 1 (`internal/pgmigrate`) | new top-level `testpg/` module |

The entire heavyweight dependency tree in `sdk/go/go.mod` (testcontainers,
pgx, moby, docker, containerd, otel, gopsutil, …) is pulled in **solely**
by `sdk/go/testpg`. The `conformance` packages import only
`github.com/google/uuid`, `google.golang.org/grpc` (+ codes/status/
credentials), and `google.golang.org/protobuf` helpers — all but `uuid`
already in `protocols/go.mod`. The `action` package additionally imports
`gopkg.in/yaml.v3` (its `Action.UnmarshalYAML` custom unmarshaler). So the
two direct deps `protocols` gains are `github.com/google/uuid` and
`gopkg.in/yaml.v3` — both pure-Go, no transitive bloat, neither
"opinionated infrastructure" (no DB driver, no test infra).

**The `foundation/locks` leak.** `foundation/locks/types.go` re-exports
**17** `claimproducer` symbols as aliases (`type ClaimID =
claimproducer.ClaimID`, … plus const/var re-exports) alongside its
genuinely-internal lock machinery (`Registry`, `ModeCoexists`, lifecycle
registry, late-bind proxies). External Go code that wanted a few contract
types reached into `foundation/locks` to get them — pulling an internal
module into a consumer.

**The `foundation/internal/pgtest` duplicate.** `foundation/internal/
pgtest/pgtest.go` carries a tracked copy of the testcontainer helper
(`@source: sdk/go/testpg/testpg.go::StartFreshPostgresDSN`, `@diverged:
false`), because `foundation/` cannot import `sdk/go` under the
`foundation-purity` lint rule. `foundation/go.mod` therefore already
requires testcontainers + pgx for this test-only helper.

**Licensing.** `licensing.yml` maps `protocols/` and `sdk/go/` to Apache;
`internal/` to AGPL. Longest-prefix-match wins. The build-step check
(`cmd/rimsky-license-check`, `make license-lint` / `make license-stamp`)
enforces per-file headers and import direction (Apache may not import
AGPL). Every `sdk/go` package that moves carries an Apache header today,
and none of the moving packages import any AGPL rimsky package (verified).

## Target design

### Module topology (after)

`go.work` ties four modules: `.` (root), `./foundation`, `./protocols`,
`./testpg`. The `sdk/go` module is deleted; the `testpg` module is added.
`testpg` is a test-only, opt-in helper module — not part of the runtime
build — and carries the testcontainers + pgx deps alone.

### `protocols` layout (after)

```
protocols/
  proto/v1/gen/    wire contract (generated)            ┐
  claimproducer/   contract ergonomics (existing)       │ CONTRACT
  lifecycle/       contract ergonomics (existing)       │  (bare names)
  action/          contract vocab (moved from sdk)      │
  conformance/     executable contract (moved from sdk) ┘
    blobbackend/ claimproducer/ dataprocessing/
    executor/ (+ executor/scenarios/) publisher/ validation/
  serverkit/       implementer scaffolding (moved)      ┐ CONVENIENCE
  publisherkit/    implementer scaffolding (moved)      ┘  (kit suffix)
```

`protocols/go.mod` gains two direct dependencies relative to today:
`github.com/google/uuid` (from `conformance`; pure-Go, already pervasive in
rimsky) and `gopkg.in/yaml.v3` (from `action`'s custom YAML unmarshaler).
grpc and
protobuf are already present. The grpc/protobuf requires currently in
`sdk/go/go.mod` merge into `protocols/go.mod`; the testcontainers/pgx
requires do **not** (they go to `testpg`).

Naming rules applied:
- **Contract** packages keep bare names: `action`, `conformance` (plus
  existing `claimproducer`, `lifecycle`, `proto`).
- **Convenience** scaffolding takes a `kit` suffix to make the
  contract-vs-convenience line visible in the import path: `serverkit`
  (was package `server`), `publisherkit` (was package `publisher`). The
  package identifiers are renamed accordingly (`server.X` → `serverkit.X`).
- `sdk/go/stores/action` loses the legacy `stores/` intermediate; it lands
  at `protocols/action`, package `action`.

### `testpg` module

A new top-level module at `./testpg/` (sibling to `protocols/`,
`foundation/`), Apache-licensed, opt-in. Houses `StartFreshPostgresDSN`
and its port-mapping retry helper, moved verbatim from `sdk/go/testpg`.
A future per-DB helper (e.g. `testsqlite`) would land as its own sibling
module so each carries only its DB's deps; not built here.

`internal/pgmigrate` (the one rimsky-internal consumer) repoints its
import from `sdk/go/testpg` to the new `testpg` module.

### `ops` relocation

`sdk/go/ops` (generic `DSNFromEnv`, slog `Setup`, `HealthHandler` — no
rimsky-specific content) is demoted to `internal/ops`, making it
core-internal and non-public. Its single in-repo consumer repoints to
`internal/ops`. Per-file headers flip Apache → AGPL (the `internal/`
prefix is AGPL); this is correct — `ops` is being de-published precisely
because it is not contract.

### Closing the `foundation/locks` contract-type leak

`protocols/claimproducer` becomes the canonical (and only) Go home for the
claim-producer contract types. All 17 `claimproducer` re-export aliases in
`foundation/locks/types.go` are removed. Rimsky-internal users that need a
contract type import `protocols/claimproducer` directly. The genuinely
internal lock machinery (`Registry`, `ModeCoexists`, lifecycle registry,
late-bind proxies) stays in `foundation/locks`. After this, no external
consumer needs `foundation` at all.

This is the largest mechanical surface of the spec. Verified: **58 files
outside `foundation/locks/` reference the aliased symbols as
`locks.<Symbol>`** (`locks.ClaimID`, `locks.Intent`, `locks.WriteSemantics`,
`locks.ClaimResult`, `locks.OpenOutcome`, `locks.Capabilities`,
`locks.ParseWriteSemantics`, the split-scope types, and the
`Intent`/`WriteSemantics` const values), concentrated in `runtime/` (~20
files) and spread across `control/controlapi`, `control/config`,
`graph/attribute`, the `cmd/*-conformance` test files, `test/scenarios/`,
and `foundation/locks/storetest/`. Each repoints `locks.<Symbol>` →
`claimproducer.<Symbol>` (adding the `protocols/claimproducer` import). In
addition, the ~7 files *inside* `foundation/locks` that use the bare
aliased names intra-package switch to importing `claimproducer` and using
`claimproducer.<Symbol>`. The lock machinery genuinely uses these contract
types, so the locks package keeps a `protocols/claimproducer` import — it
just no longer *re-exports* them.

### `foundation/internal/pgtest` — keep the tracked duplication

`foundation/internal/pgtest` stays a tracked duplicate of the testpg
helper; only its `@source:` annotation repoints from
`sdk/go/testpg/testpg.go` to the new `testpg/testpg.go`. Rationale:
`foundation` importing the `testpg` module would introduce a
foundation→testpg module dependency (a `replace` directive and a
cross-module test coupling) for a ~40-line helper; the duplication is
already tracked and kept byte-identical via `@source`/`@diverged`. DRYing
the two copies is an explicit non-goal (noted under "Follow-ups").

### Lint-rule changes (`.golangci.yml` depguard)

- **`sdk-purity` → `protocols-purity`.** Rename the rule and retarget its
  `files:` from `**/sdk/go/**` to `**/protocols/**`. It denies imports of
  every rimsky-internal layer (`foundation`, `internal`, `graph`,
  `runtime`, `control`, `cmd`). This is the structural guard that keeps
  the contract module from importing anything opinionated.
- **`pgx-isolation` allow-list.** Replace the `!**/sdk/go/**` exclusion
  with `!**/testpg/**`; update the rule's `desc` strings to name `testpg/`
  instead of `sdk/go/`. `protocols` stays *out* of the allow-list — the
  contract module must not import pgx (verified: nothing moving in does).
- **`foundation-purity`.** Remove the `deny` entry for
  `github.com/rimsky-ai/rimsky-core/sdk/go` (the module no longer
  exists). The existing foundation→graph/runtime/control/cmd/stores/
  executors denials are unaffected.
- **`graph-purity`.** Remove the `deny` entry for `…/sdk/go`.
- **`consumption-side-isolation`.** Update the rule's comment to drop the
  "(post-P2) sdk/go" reference; consumption-side binaries now implement
  against `protocols/` only. No `deny`-list change.

Optionally (belt-and-suspenders), `protocols-purity` may also deny
`testcontainers-go` to make "no test infra in the contract module"
compiler-checked rather than relying on `protocols/go.mod` simply not
requiring it. Include this deny.

### Licensing (`licensing.yml`)

- **Remove** the `sdk/go/` entry from the `apache:` block (directory
  deleted).
- **Add** `testpg/` to the `apache:` block — opt-in adopter-facing test
  infra, same tier as the old `sdk/go/` testpg helper.
- The moved `conformance`, `action`, `serverkit`, `publisherkit` code
  lands under the existing `protocols/` Apache prefix — no new entries.
- `internal/ops` is covered by the existing `internal/` AGPL prefix —
  re-stamp the moved `ops` files Apache → AGPL via `make license-stamp`.
- **Remove the stale bare `conformance/` entry** from the `apache:` block:
  there is no top-level `conformance/` directory (it was the retired
  legacy repo-root conformance dir from the 2026-05-24 SDK extraction).
  Pre-existing dead entry; clean it up.

The move is boundary-clean: all moving code is already Apache, lands under
an Apache prefix, and imports no AGPL rimsky package, so the
import-direction lint stays green.

## Repoint scope

Two distinct repoints: the `sdk/go/*` importers, and the `foundation/locks`
contract-type consumers (the larger surface — see "Closing the
`foundation/locks` … leak" above for the 58-file `locks.<Symbol>` →
`claimproducer.<Symbol>` migration plus the intra-`locks` references).

`sdk/go/*` importers — verified external (non-`sdk/go`) import-site counts
by subpackage:

- `sdk/go/stores/action` (13) → `protocols/action`: the 2 store
  testfixtures (`stores/{filesystem,postgres}/testfixture`), the stub
  store (`stores/stub/cmd/main.go` and `stores/stub/store/{store.go,
  store_test.go}`), and 8 scenario tests under `test/scenarios/`.
- `sdk/go/conformance/*` → `protocols/conformance/*`: the 7
  `cmd/rimsky-*-conformance` binaries.
- `sdk/go/server` (2) → `protocols/serverkit`: the stub executor
  (`executors/stub/cmd`) and stub store (`stores/stub/server`).
- `sdk/go/testpg` (1) → `testpg` module: `internal/pgmigrate`.
- `sdk/go/ops` (1) → `internal/ops`.
- `foundation/internal/pgtest`: `@source:` annotation repoint only (not an
  import) — **both** annotations (`StartFreshPostgresDSN` and
  `resolveConnectionString`) repoint from `sdk/go/testpg/testpg.go::<fn>`
  to `testpg/testpg.go::<fn>`.

`go.work` drops `./sdk/go`, adds `./testpg`. Each touched module is
re-tidied (`make tidy`).

## Testing / verification strategy

Per the project's "After Code Changes" rule, the change is not done until
all of the following pass:

- `go build ./...` and `go test ./...` green across **every** module
  (root, foundation, protocols, testpg).
- `make lint` green — confirms `protocols-purity` holds (protocols imports
  no rimsky-internal layer), `pgx-isolation` holds (pgx confined to the
  allow-list including `testpg`, excluded from `protocols`), and the
  retargeted foundation/graph purity rules pass.
- `make tidy` leaves all module manifests clean.
- Scenario + storage tests (testcontainers-backed; Docker required):
  `go test ./test/scenarios/... ./foundation/persistence/... -count=1`.
- Race-sensitive paths unaffected by topology, but run once for safety on
  the packages whose imports moved.
- Conformance suites pass: build all 7 `cmd/rimsky-*-conformance` binaries
  and run them against the relevant executors/producers (the executor and
  claim-producer runners at minimum), confirming the relocated conformance
  library is wire-equivalent.
- `make license-lint` green and `make license-stamp` leaves no diff
  (headers correct after the `ops` Apache→AGPL flip and the `testpg`
  Apache stamp).

## Design changes

(Concept-doc and design-surface mutations `execute-plan` applies alongside
the code. New concept-body text below is path-free per the concept
self-containment rule; the mutation *instructions* name paths freely.)

- **Concept: retire `concept:sdk`.** Move
  `.ok-planner/design/concepts/sdk.md` to
  `.ok-planner/design/concepts/_retired/sdk.md` and add it to the "Retired
  concepts" list in `.ok-planner/design/concepts.md` with a one-line
  retirement summary. Retirement note (append to the retired file's Notes):
  `2026-05-26 — Retired per spec:2026-05-26-collapse-sdk-into-protocols.
  The separate Go SDK module dissolved into the protocols module: the
  server-side and publisher-side implementer scaffolding, the claim-
  producer action vocabulary, and the conformance library all moved into
  the protocols module; the ops glue was demoted to a rimsky-internal
  package; the Postgres testcontainer helper was carved into its own
  opt-in test-helper module. For Go there is no separate SDK — the
  protocols module is the single public surface a service implementer
  imports. A future development kit serves a different purpose, is
  Python-first, and sits as an authoring layer above the contract; it is
  not a Go SDK successor and must not reintroduce a satellite Go module.`

- **Concept: mutate `concept:module-layout`** in place
  (`.ok-planner/design/concepts/module-layout.md`):
  - In "What it is": replace the "SDK module" bullet with a "Postgres-
    test-helper module" bullet — *an opt-in, test-only module that carries
    the Postgres testcontainer helper and its testcontainers + Postgres-
    driver dependencies alone, so the contract module and every consumer
    that does not want a Postgres test container stay free of those
    dependencies.* Expand the "Protocols module" bullet to: *the service-
    protocol interfaces and protobuf bindings, the hand-written contract
    ergonomics, the claim-producer action vocabulary, the implementer-
    facing server-side and publisher-side scaffolding, and the conformance
    library. It is the single public Go module a service implementer
    imports. Dependency budget: stdlib plus the gRPC, protobuf, UUID, and
    YAML libraries only — no database driver, no test infrastructure.* In the
    "Root module" bullet, change the plain-Postgres-fixture sentence to
    say the plain-Postgres fixture now lives in the Postgres-test-helper
    module.
  - In "Purpose": replace the sentence directing implementers who want
    scaffolding/conformance to import the SDK module with: *An implementer
    who wants the paved-path server scaffolding, publisher helpers, or the
    conformance library imports the same protocols module — for Go there is
    no separate SDK module.*
  - In "Boundaries": drop `sdk` from the Adjacent list; remove the clause
    naming the locks package's aliasing of protocols types from the "Owns"
    sentence (the locks package no longer aliases contract types; the
    canonical Go home is the claim-producer contract package).
  - In "Invariants": replace the `sdk-purity` invariant with: *The
    protocols-purity rule denies the protocols module from importing any
    rimsky-internal layer (foundation, internal, graph, runtime, control,
    cmd) or test infrastructure. The protocols module is the public
    contract surface; its dependency budget is stdlib plus the gRPC,
    protobuf, UUID, and YAML libraries.* Update the pgx-isolation invariant to
    say the allow-list includes the Postgres-test-helper module (not the
    SDK module). In the foundation-purity and graph-purity invariants,
    delete the "or the SDK" clause.
  - Two residual SDK references elsewhere in the body must also change.
    In the control-layer bullet's parenthetical, change "the four modules
    in the workspace post-2026-05-24 are protocols, foundation, SDK, and
    root" to "…are protocols, foundation, the Postgres-test-helper module,
    and root." In the layer-ordering paragraph, replace the sentence "The
    SDK module imports only protocols + stdlib + minimal third-party
    (enforced by the sdk-purity rule); it is implementer-facing and never
    consumes rimsky-internal layers." with: *The protocols module imports
    only stdlib plus the gRPC, protobuf, UUID, and YAML libraries (enforced
    by the protocols-purity rule); it is the public contract surface and
    never consumes rimsky-internal layers.*
  - Append a Notes entry: `2026-05-26 — the SDK module collapsed into the
    protocols module; the Postgres testcontainer helper was carved into its
    own opt-in test-helper module; the sdk-purity lint rule became
    protocols-purity; the claim-producer contract-type aliases were removed
    from the locks package (canonical home is the claim-producer contract
    package). Per spec:2026-05-26-collapse-sdk-into-protocols.`

- **Concept: mutate `concept:conformance`** in place
  (`.ok-planner/design/concepts/conformance.md`):
  - "What it is": change "Six thin CLI wrappers (one per protocol) over a
    shared SDK conformance library" to "…over a shared conformance library
    in the protocols module"; change "The SDK conformance library lives in
    the peer Go module" to "The conformance library lives in the protocols
    module."
  - In the blob-backend bullet: change "adapts each concrete backend … to
    the SDK's reduced backend interface so the in-process suite stays
    SDK-purity-clean" to "…to the conformance library's reduced backend
    interface so the in-process suite stays protocols-purity-clean."
  - Invariant: change "compile-time dependency on the protocols module plus
    the SDK Go module via the workspace" to "compile-time dependency on the
    protocols module."
  - Boundaries: drop `sdk` from the Adjacent list. In the same Boundaries
    body, change "Owns: the SDK conformance library, the thin CLI
    wrappers…" to "Owns: the conformance library, the thin CLI wrappers…",
    and change "backed by a lifecycle-check entry point in the SDK library"
    to "…in the conformance library."
  - Append a Notes entry: `2026-05-26 — conformance library moved from the
    SDK module into the protocols module as a sub-package; no API change.
    Per spec:2026-05-26-collapse-sdk-into-protocols.`

- **Annotations:** the `@concept: sdk` annotation in `sdk/go/doc.go`
  disappears with the deleted module (consistent with retiring the
  concept). The `@concept: conformance` annotation rides along with
  `sdk/go/conformance/executor/runner.go` to its new path under
  `protocols/conformance/executor/`; slug unchanged, no repoint.

**Tensions: none.** No open tension matches this refactor; none is
resolved or raised. (The deferred `internal/`-fencing is recorded as a
spec follow-up below, not catalogued as a tension.)

## Follow-ups (out of scope)

- **`internal/`-fencing of `foundation`.** Once external consumers have
  migrated off the old layout, push the now-private foundation packages
  under `internal/` so external import is a compile error rather than a
  convention. Breaking; sequence after the external-consumer migration.
- **External-consumer migration.** Any external Go consumer of the old
  layout repoints onto the new one: `foundation/locks` → `protocols/
  claimproducer` for contract types; the old SDK action-vocab path →
  `protocols/action`; `replace` directives and any build-context
  references updated. This is the consumer's own-repo task, gated on this
  spec landing — not a rimsky-repo task.
- **DRY `foundation/internal/pgtest` against `testpg`.** Possible once
  `testpg` is a standalone module, but introduces a foundation→testpg
  cross-module test dependency; deliberately not done here.
- **`testsqlite` sibling module.** Add a per-DB SQLite test helper as its
  own opt-in module if/when SQLite test ergonomics warrant it.

## Decisions recorded (from brainstorm)

1. Part 4 of the sketch taken as settled (fold `server`/`publisher`/
   `stores/action` into `protocols`; demote `ops`; carve `testpg`; delete
   `sdk/go`; close the `locks` leak).
2. `conformance` → sub-package of `protocols` (deps are light: the move
   adds only `google/uuid` from `conformance` and `gopkg.in/yaml.v3` from
   `action`; the heavy tree was all `testpg`).
3. `testpg` → top-level `testpg/` module at the repo root.
4. Package naming: `serverkit` + `publisherkit` (convenience, kit suffix);
   `action` + `conformance` (contract, bare names).
5. `internal/`-fencing of `foundation` → deferred (out of scope).
6. RDK framing → brief durable note on the retired `concept:sdk`; no RDK
   concepts created, no coupling to the RDK sketch.
7. `protocols` → Apache-2.0 (stays); the collapse is licensing-boundary-
   clean; stale `conformance/` entry in `licensing.yml` removed.
