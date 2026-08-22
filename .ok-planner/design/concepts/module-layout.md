---
concept: module-layout
---

# Module layout

## What it is

The module layout is the shape of rimsky's source tree: one Go workspace tying four modules into one build, and four groups of code at the top of the repository. The four code groups are the binaries, the shippable library code, the out-of-tree tests with their machinery, and the dev tooling. Entries at the top that hold build inputs and release artifacts sit alongside the four groups and belong to none of them. The four modules are:

- **Protocols module** — the service-protocol interfaces and their generated bindings, the hand-written contract ergonomics, the claim-producer action vocabulary, the scaffolding a service implementer needs on the server side and the publisher side, and the conformance library. It is the one public module a service implementer imports.
- **Foundation module** — primitives only: the cascade engine, the claim and lock primitives, the persistence drivers, the shared infrastructure types, and the state-machine enums. It stands on its own and reaches back into no module above it.
- **Services module** — the bundled consumption-side services rimsky ships as images: the claim-producer stores, the sensors, the lifecycle subscribers, and the executors. The layered library code never imports it. The binaries do import it, so a bundled handler registers in process where one process runs every role. That edge runs one way.
- **Root module** — the graph, runtime, and control layers of the library code, plus the binaries, the dev tooling, and the test-support scaffolding.

The root module's library code splits into three layers above the foundation:

- **Graph layer** — the cascade model: templates, instances, frames, attributes, and the scheduler step functions the runtime layer's loop calls.
- **Runtime layer** — the bridge: the supervisor runner, the conductor, the scheduler tick loop and the settlement pass it drives, the sweeps, the orphan reapers, auto-terminal, the terminal-decision engine, the callback server, the clients that call peer services, and the in-process registries for executors and for claim producers.
- **Control layer** — the operator surfaces: the control API, observability, and configuration loading. The command-line library sits with the binaries, and the operator's model-context shim belongs to the root module rather than a module of its own.

The layers stack in one order: foundation, then graph, then runtime, then control. Each layer reads the layers below it. The protocols module reads no rimsky-internal layer at all, because it is the public contract surface.

## Purpose

The layout keeps an implementer's import surface small. An implementer of a service protocol imports one module, and that module also carries the server scaffolding, the publisher helpers, and the conformance library, so there is no second module to adopt. The heavier libraries the root module pulls never reach that implementer. The layer split isolates the bridge concerns — the supervisor, the sweeps, the peer clients — in the runtime layer, so the cascade model in the graph layer stays a clean dependency target. The grouping at the top of the repository keeps the shippable library code separable from the binaries, the tests, and the dev tooling.

## Boundaries

The module layout owns the per-module manifests, the workspace definition that ties the four modules together, the rules that keep each layer pure, the layer ordering, the four-way grouping of code at the top of the repository, and the home of the bundled services under the library code (see `decision:module-split`, `decision:layer-ordering`, `decision:toplevel-dirs`). The protocols module owns the surface an implementer sees; it does not own the calling side, which stays with the peer clients in the runtime layer. The split between the permissive surface and the copyleft surface is a licensing choice (see `decision:licensing-dual-apache-agpl`).

The module layout does not own the layout inside a package, which each feature decides. It does not own the wire content of the protocols, which the protocols module owns. It does not own the top-level entries that hold build inputs and release artifacts. See also: `persistence-database`, `claim-producer`, `executor`, `lifecycle-subscriber`.
