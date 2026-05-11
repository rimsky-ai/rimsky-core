---
concept: licensing-boundary
status: as-is
aliases: []
references:
  - _discover/licensing-boundary-map.md
---

# Licensing boundary

## What it is

The codebase is dual-licensed. `licensing.yml` at the repo root is the per-directory map (Apache 2.0 vs AGPL-3.0-or-later). Path classification: longest-prefix-match wins. `cmd/rimsky-license-check` is the enforcement tool (`make license-lint` + `make license-stamp`).

Apache: `protocols/`, `foundation/locks/`, `foundation/integration/remote/`, `foundation/shared/`, `conformance/`, `stores/`, `executors/`, modeling subdirs that are public types (cli, executor, node, shared, template/canonical, qualityrule spec), CLI / conformance / docs `cmd/` binaries, dashboards, deploy, docs.

AGPL: foundation cascade/integration/persistence/internal, modeling attribute/config/controlapi/frame/internal/observability/qualityrule/eval/scenario/scheduler, runtime `cmd/` binaries (scheduler / supervisor / control-api / migrate / entrypoint), `test/`.

## Purpose

External implementers of the three protocols depend on Apache-only code; runtime cores are AGPL so anyone hosting modified rimsky for others is bound by AGPL's network-use copyleft.

## Boundaries

Owns: `licensing.yml`, the per-file header stamping, the import-direction lint. Does NOT own: dependency-tree licensing of transitive Go libs (that's a separate compliance concern). Adjacent: `module-layout`, `quality-rule` (interesting Apache spec / AGPL eval split).

## Invariants

- Longest-prefix-match: new code under an AGPL directory is AGPL unless explicitly overridden.
- Apache code cannot import AGPL code (enforced by `make license-lint`).
- `modeling/qualityrule/` is Apache; `modeling/qualityrule/eval/` is AGPL — same conceptual feature split by stability.
- `foundation/integration/remote/` is an Apache override under the AGPL `foundation/integration/` tree (it's the wire-protocol-surface gRPC client).

## Aliases and historical names

None live.

## Open within this concept

(no specific live tensions)

