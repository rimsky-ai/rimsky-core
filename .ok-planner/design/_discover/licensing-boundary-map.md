---
topic: licensing-boundary-map
kind: boundary
---

# Dual-licensed codebase: `licensing.yml` declares Apache/AGPL per-directory; `cmd/rimsky-license-check` enforces

## Description

Rimsky is dual-licensed. Some packages are Apache 2.0 (permissive, including the public protocol surface and bundled reference impls); others are AGPL-3.0-or-later (the runtime cores: foundation, modeling, the cmd binaries for scheduler/supervisor/control-api). A commercial license is also available (`COPYRIGHT`).

`licensing.yml` at the repo root is the single source of truth for the per-file license headers and the import-graph lint enforced by `cmd/rimsky-license-check/`. Path classification rule: **longest-prefix-match wins**. Subdirectories of AGPL paths can be reclassified Apache by being listed under the `apache:` block.

The current map (from `licensing.yml`):

**Apache 2.0**:

- `protocols/` — entire interface module.
- `foundation/locks/` — Apache-island inside foundation.
- `foundation/integration/remote/` — the gRPC client for ClaimProducer (override under foundation AGPL).
- `foundation/shared/` — Apache shared types.
- `modeling/cli/`, `modeling/executor/`, `modeling/node/`, `modeling/shared/`, `modeling/template/canonical/`, `modeling/qualityrule/` (spec types only).
- `conformance/`, `stores/`, `executors/` — bundled reference impls.
- `cmd/rimsky-cli/`, `cmd/rimsky-executor-conformance/`, `cmd/rimsky-claim-producer-conformance/`, `cmd/rimsky-conformance-probe/`, `cmd/rimsky-license-check/`, `cmd/rimsky-docs-*/`.
- `dashboards/`, `deploy/`, `docs/`, `cold-read/`.

**AGPL-3.0-or-later**:

- `foundation/cascade/`, `foundation/integration/` (except `remote/`), `foundation/persistence/`, `foundation/internal/`.
- `modeling/attribute/`, `modeling/config/`, `modeling/controlapi/`, `modeling/frame/`, `modeling/internal/`, `modeling/observability/`, `modeling/qualityrule/eval/` (runtime evaluators only), `modeling/scenario/`, `modeling/scheduler/`.
- `cmd/rimsky-scheduler/`, `cmd/rimsky-supervisor/`, `cmd/rimsky-control-api/`, `cmd/rimsky-migrate/`, `cmd/rimsky-entrypoint/`.
- `test/`.

**Exempt** (no per-file header): LICENSE.*, COPYRIGHT, NOTICE, TRADEMARKS.md, CLA.md, CONTRIBUTING.md, licensing.yml itself, go.mod, go.sum, go.work, go.work.sum.

`cmd/rimsky-license-check/` is a binary that does two things (per `licensing.yml` "Update procedure"):

1. `make license-lint` — verifies import direction (Apache code can't import AGPL).
2. `make license-stamp` — updates per-file headers to match `licensing.yml`.

The split lets external implementers of the three protocols depend on Apache-only code (the protocols module is fully Apache; the foundation Apache-island at `foundation/locks/` carries the `ClaimSpec`/`ClaimResult` aliases that a Go-side producer-author needs). Runtime cores (foundation/integration, modeling) are AGPL — anyone hosting modified rimsky binaries for others is bound by AGPL's network-use copyleft.

`modeling/qualityrule/` is the most interesting split: the spec types are Apache (so a template author or third-party tool can consume them without AGPL), but the runtime evaluators are AGPL. The `licensing.yml` comment is explicit: "spec types only; runtime is at modeling/qualityrule/eval/ (AGPL)."

`foundation/integration/remote/` is the other notable override: the gRPC client for `ClaimProducer` is Apache despite living in the AGPL-by-default `foundation/integration/` directory. This is because the client code is part of the wire-protocol surface — anyone who runs a custom producer needs a sample client.

The CLA.md (Contributor License Agreement) governs incoming contributions; contributors retain copyright but grant the project the right to relicense.

## Code surface

- `licensing.yml` — entire file (~80 lines).
- `cmd/rimsky-license-check/main.go` — entry point.
- `cmd/rimsky-license-check/walker.go` — file-tree walker.
- `cmd/rimsky-license-check/config.go` — yaml loader.
- `cmd/rimsky-license-check/headers.go` — header templates.
- `cmd/rimsky-license-check/imports.go` — import-direction enforcement.
- `LICENSE.apache`, `LICENSE.agpl`, `COPYRIGHT`, `NOTICE`, `TRADEMARKS.md`, `CLA.md`, `CONTRIBUTING.md`.

## Prose surface

- `licensing.yml` (comments) — rationale + update procedure.
- `docs/history/2026-05-02-licensing-design.md` (referenced in `licensing.yml`) — design rationale.
- `CONTRIBUTING.md` — CLA reference for contributors.

## Adjacent topics

- `2026-05-10-three-go-module-split` — module split aligns with licensing (protocols entirely Apache).
- `quality-rules-and-attribute-validation` — Apache/AGPL split inside qualityrule.
- `2026-05-10-conformance-test-binaries` — all conformance binaries are Apache.

## Observations

- The longest-prefix-match rule means a future addition under `foundation/integration/` defaults to AGPL; an Apache override needs an explicit entry. This is the conservative direction (new code stays AGPL unless explicitly chosen otherwise).
- The Apache-side `cmd/` binaries are all "tooling" (CLI, conformance, license-check, docs-gen); the AGPL-side `cmd/` binaries are the runtime workhorses. The decision boundary is "is this software a runtime component or a developer tool?"
- `cmd/rimsky-license-check` is mentioned as the enforcement tool, but its actual integration into CI is via `make license-lint`. A breakage of the import direction (AGPL imported into Apache code) fails the lint.
- The licensing design doc (`docs/history/2026-05-02-licensing-design.md`) is referenced in `licensing.yml` but lives in the historical doc tree; it may have been archived under `.ok-planner/archive/`.
