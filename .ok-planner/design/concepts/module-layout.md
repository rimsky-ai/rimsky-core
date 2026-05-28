---
concept: module-layout
status: as-is
aliases:
  - workspace-layout
references:
  - _discover/2026-05-10-three-go-module-split.md
  - _discover/2026-05-10-depguard-enforced-package-boundaries.md
  - _discover/2026-05-10-stdlib-slog-and-minimal-deps.md
  - _discover/licensing-boundary-map.md
---

# Module layout

## What it is

The Go workspace ties three modules into one build. The repo root holds four idiomatic top-level code directories: binaries (the cmd group), shippable library code (the lib group), out-of-tree tests plus their machinery (the test group), and dev tooling (the tools group). Library code under the lib group keeps the four-layer split (graph + runtime + control, consumed by the cmd binaries) established by the 2026-05-13 four-layer restructure:

- **Protocols module** (under the lib group) — the service-protocol interfaces and protobuf bindings, the hand-written contract ergonomics, the claim-producer action vocabulary, the implementer-facing server-side and publisher-side scaffolding, and the conformance library. It is the single public Go module a service implementer imports. Dependency budget: stdlib plus the gRPC, protobuf, UUID, and YAML libraries only — no database driver, no test infrastructure.
- **Foundation module** (under the lib group) — primitives only. Cascade engine, claim/lock primitives, persistence drivers, shared infrastructure types (clocks, loggers, ID generation, JSON merge), and the state-machine enums (node-state, last-outcome, illegal-transition error). Depends on the protocols module plus a Postgres driver, a UUID library, and a pure-Go SQLite driver. The foundation module is self-contained — it declares no replace directive against the root module.
- **Root module** — the graph, runtime, and control layers (under the lib group) plus the cmd binaries and the dev tooling. Pulls heavier libraries (JSON-schema validation, cron parsing, JCS canonicalization, testcontainers). The test-support scaffolding belongs to the root module too: a migrations-applying Postgres test fixture, a plain-Postgres testcontainer helper (formerly its own opt-in module, demoted to a plain test-support package in the 2026-05-27 reorg), the stub claim-producer + stub executor test doubles, and the end-to-end scenario harness all live under the test group.
  - **graph layer** — cascade model: templates, instances, frames, attributes, scheduler. Imports foundation + protocols. (The scenario harness, formerly filed here, now lives under the test group.)
  - **runtime layer** — bridge layer: supervisor runner, conductor, sweeps, orphan reapers, auto-terminal, terminal-decision engine, callback server, the peer-client layer (gRPC clients to claim-producer / lifecycle-subscriber / publisher / validation / data-processing service impls — renamed from "remote" in 2026-05-24), and the executor gRPC client pool. Imports foundation + graph + protocols.
  - **control layer** — operator surfaces: the control API, observability, and config loading. Imports runtime + graph + foundation + protocols. The CLI library, formerly filed under the control layer, now lives alongside the rimsky binary in the cmd group. The operator MCP shim is part of the root module, NOT a separate Go module.

Layer ordering: foundation → graph → runtime → control. The control layer reads everything below it; the graph layer never reads the runtime or control layers (one-way, lint-enforced by the graph-purity rule); the runtime layer never reads the control layer (runtime-purity); the foundation layer never reads the graph, runtime, or control layers (foundation-purity). The protocols module imports only stdlib plus the gRPC, protobuf, UUID, and YAML libraries (enforced by the protocols-purity rule); it is the public contract surface and never consumes rimsky-internal layers.

One documented residual (a per-site lint exemption; flagged for separate follow-up):

- The scheduler in the graph layer imports the runtime layer for the sweep entry points the scheduler tick orchestrates.

The previously-documented foundation → graph back-import (the persistence layer importing graph node types for the persistable row types) was eliminated in the 2026-05-13 back-import cleanup cycle: the persistable row-type primitives moved into a new foundation spec package; the per-site lint exemptions are retired; foundation-purity applies unconditionally.

## Purpose

Layered import-budget discipline. An external implementer of the claim-producer protocol imports only the protocols module. An implementer who wants the paved-path server scaffolding, publisher helpers, or the conformance library imports the same protocols module — for Go there is no separate SDK module. The root module pulls heavier libraries that implementers never see transitively. The four-layer split inside the root module's library code isolates the bridge concerns (supervisor + sweeps + peer clients) in the runtime layer so the cascade-model code in the graph layer stays a clean dependency target. The root's top-level directory grouping — binaries, library code, out-of-tree tests + their machinery, and dev tooling — keeps the shippable surface (library code) cleanly separable from the binaries, the test scaffolding, and the dev tooling.

## Boundaries

Owns: the per-module manifests, the workspace definition, the layer-purity lint rules, the four-layer ordering inside the root module's library code, the four-way top-level directory grouping (binaries / library code / out-of-tree tests + machinery / dev tooling), and the three-module workspace (protocols, foundation, root). The protocols module owns the implementer-facing surface; it does NOT own the calling-side wire code (rimsky-internal, stays in the runtime peer layer). Does NOT own: package-internal layout (that's per-feature), proto wire content (owned by the protocols module). Adjacent: `persistence-database`, `claim-producer`, `executor`, `lifecycle-subscriber`.

## Invariants

- The pgx-isolation lint rule denies the Postgres driver outside an allow-list (which includes the test-support Postgres testcontainer helper and the migrations-applying test fixture so they can use it).
- The foundation-internal-isolation rule denies imports of the foundation module's internal packages from outside the foundation module.
- The protocols-purity rule denies the protocols module from importing any rimsky-internal layer (foundation, internal, graph, runtime, control, cmd) or test infrastructure. The protocols module is the public contract surface; its dependency budget is stdlib plus the gRPC, protobuf, UUID, and YAML libraries.
- The consumption-side-isolation rule denies the bundled stores, sensors, subscribers, and executors from importing any rimsky-internal layer. Introduced during P1 of the 2026-05-24 reorganization as the defensive guard that lets consumption-side bundled deliverables move out cleanly; stays in place post-move as a re-bundling guard. The in-repo stub claim-producer and stub executor (test-infrastructure carve-outs that stayed in rimsky) are exempt, so they may consume rimsky-internal helpers.
- The foundation-purity rule denies the foundation layer from importing the graph, runtime, control, or cmd layers. Applies unconditionally — there are no per-site exemptions.
- The graph-purity rule denies the graph layer from importing the runtime, control, or cmd layers. Per-site exemption for the scheduler → runtime. (The scenario harness, which boots the full stack, moved out of the graph layer into the test-support group in the 2026-05-27 reorg, so it is no longer subject to graph-purity at all.)
- The runtime-purity rule denies the runtime layer from importing control or cmd.
- (Retired: the graph-control-isolation rule; subsumed by graph-purity.)
- Logging is stdlib structured logging only; HTTP routing, the Postgres driver, the SQLite driver (pure-Go, no CGO), and cron parsing are each pinned to a single library. Resist adding heavier alternatives.
- The three runtime processes (scheduler, supervisor, control-api) never import each other; cross-process state flows through Postgres only.

## Aliases and historical names

Pre-layer-crystallization (`spec:2026-05-04-layer-crystallization-design`), the codebase was a single Go module. Between the 2026-05-12 nomenclature resolution and the 2026-05-13 four-layer restructure, the root module had a two-way graph + control split with a foundation integration package (which imported back into the root via a replace directive) carrying the supervisor and sweeps; that package was moved to the runtime layer at the root module, and the executor client moved from the graph layer to the runtime layer. The graph layer retained graph-specific policy enums (the quality-rule severity, the backoff-kind, the jitter-kind, the access-kind); the shared infrastructure primitives (clocks, loggers, ID generation, JSON merge) moved to the foundation shared package and the state-machine enums (node-state, last-outcome, illegal-transition error) moved to the foundation cascade package. The 2026-05-24 repo-reorganization renamed the runtime "remote" client layer to "peer" (the name "remote" implied an externally-facing surface, but the layer is rimsky-internal infrastructure tightly coupled to `concept:supervisor`, `concept:terminal-resolution`, and `concept:discovery-cache` — "peer" matches the `concept:service` vocabulary better).

## Licensing boundary

A per-directory Apache-2.0-vs-AGPL-3.0 mapping, enforced by a build-step license check with longest-prefix-match-wins. The Apache surface is the protocols module — the wire contract a consumer implements or links against — plus a documentation carve-out; the protocols module imports nothing internal, so the Apache code forms a single closed island with no path to AGPL source. Everything else Rimsky ships (the foundation, graph, runtime, and control layers, the binaries, the dev tooling, and the test-support scaffolding) is AGPL-3.0-or-later. The Postgres testcontainer helper, formerly an Apache-licensed standalone module, was reclassified AGPL when it was demoted to test-support in the 2026-05-27 reorg — it is no longer a public module. Repo-organization concern; not a runtime noun. The check is build-step enforcement, not runtime.

(Adjacent: previously documented as a standalone concept; folded here under `2026-05-11-design-log-convergence`.)

## Open within this concept

(no specific live tensions distinct from `persistence-database`)

## Notes

- **2026-05-13: four-layer restructure.** Split the root module into the graph → runtime → control ordering. The foundation integration package moved to the runtime layer; the executor client moved from the graph layer to the runtime layer. The foundation module lost its replace directive against the root module — foundation is self-contained except for one documented residual (the persistence layer reading graph node row types) allowed via per-site lint exemption. New layer-purity lint rules added: foundation-purity, graph-purity, runtime-purity. Retired: the graph-control-isolation rule (subsumed by graph-purity).
- **2026-05-13: foundation → graph back-import eliminated.** The persistable row-type primitives (template spec, node def, evaluator state, error-type policy, quality-rule spec, the quality-rule severity, the backoff kind, the jitter kind, frame-resolution + resolve constants, etc.) moved out of the graph node package into a new foundation spec package. The graph algorithms that operate on these types (evaluate, holding-subgraphs, template-validate, required-stores) remain in the graph layer; the graph packages keep type-aliases pointing at the foundation spec package for backward compatibility. Foundation is now fully self-contained (its module tidies clean on its own); foundation-purity applies unconditionally.
- **2026-05-15: bundled-deliverables expansion.** Three new top-level consumption-side directories joined the existing bundled stores/executors/dashboards: bundled sensor-protocol reference impls (cron, HTTP, object-store, webhook), bundled lifecycle-subscriber-protocol reference impls (OpenLineage), and an examples directory (reference impls demonstrating patterns, e.g. an atomic-staging filesystem producer). Each is consumption-side (binary or example), consumes the foundation + protocols + root modules via the workspace but is not imported back into the layered packages. The pgx-isolation lint rule was extended to allow the Postgres driver in the bundled sensors and subscribers (cron-sensor state DB, OpenLineage cursor state DB). Also retired: the quality-rule package (replaced by the verifier-executor pattern), the per-node `schedule:` template field (replaced by the cron sensor), and the schedules table.
- 2026-05-24: in-repo audit prep. P1 of `spec:2026-05-24-repo-reorganization-design`: cosmetic locks → claim-producer-protocol import swaps in the bundled stores, the white-box OpenLineage subscriber test rewritten as peer-driven integration, several sensor/store tests dropped their migrations-fixture dependency, a new consumption-side-isolation lint rule added, two empty verifier-binary directories deleted.
- 2026-05-24: SDK birth + bundled-deliverables migration. P2–P6 of `spec:2026-05-24-repo-reorganization-design`: a new SDK Go module (server scaffolding, publisher helpers, conformance library, testcontainer helpers, ops glue); rimsky's calling-side "remote" client layer renamed to "peer"; conformance CLI binaries became thin wrappers over the SDK's conformance library; production-side bundled stores, sensors, the OpenLineage subscriber, and production-side executors moved to the consumption side, outside the platform. Test-infrastructure carve-outs (stub stores/executors, store testfixtures) stayed in rimsky. The boundary the reorganization drew: rimsky owns the platform and its test doubles; consumption-side deliverables (production-side bundled services, public docs and docs-tooling, the atomic-staging example and several of its scenario tests, the demo app, the dashboard) live outside the platform. In-tree scenario canaries were added to replace the moved-out drift signals.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-26 — the SDK module collapsed into the protocols module; the Postgres testcontainer helper was carved into its own opt-in test-helper module; the sdk-purity lint rule became protocols-purity; the claim-producer contract-type aliases were removed from the locks package (canonical home is the claim-producer contract package). The consumption-side-isolation rule gained an exemption for the in-repo stub claim-producer and stub executor (test-infrastructure carve-outs) so they may consume rimsky-internal helpers. Per spec:2026-05-26-collapse-sdk-into-protocols.
- 2026-05-27 (spec: none — root-folder reorg): collapsed the repo into cmd/ lib/ test/ tools/; protocols+foundation moved under lib/ (module paths gained a lib/ segment); testpg demoted from a standalone module to test/support/testpg; internal/ops deleted (dead); internal/pgmigrate + stores + executors stubs + the scenario harness consolidated under test/support/; control/cli relocated into cmd/rimsky/cli; conformance binaries folded into "rimsky conformance <protocol>" subcommands; license-check moved to tools/.
