---
tension: scenario-harness-drop-from-catalog
category: overloaded
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - scenario-harness
  - conformance
resolution:
  shape: drop-from-catalog
  dropped: concepts/scenario-harness.md
  no-fold: true
  summary: |
    Dropped scenario-harness as a standalone concept. It is in-repo
    test scaffolding (modeling/scenario/harness.go), not a runtime
    noun. The harness usage is documented in CLAUDE.md "Build & test"
    and test/scenarios/ is grep-discoverable. Conformance.md Adjacent
    reworded to remove the dangling slug.
---

# `scenario-harness` is in-repo test scaffolding, not a runtime noun

## What is muddy

`concepts/scenario-harness.md` documents `modeling/scenario/harness.go` — the in-repo entry point for scenario tests (testcontainers-Postgres bring-up, peer launch, supervisor + scheduler + control-api wiring). The doc is structurally a "how to write scenario tests" reference: invariants are about test hygiene (own Postgres container, don't reach into `foundation/internal/`, race + count flake-hunt). The concept catalog otherwise traffics in nouns the runtime code reasons about; the scenario harness is build-time test infrastructure with no runtime presence.

`conformance` is a sibling concept but the audiences differ: scenario-harness is for in-repo invariant regression coverage; conformance is for third-party peer implementers running wire-compat binaries. Folding them would force the conformance concept to split internally by audience.

## Why it matters

- Catalog scope: the concept catalog should be nouns runtime code reasons about. Test fixtures and harnesses are tooling, not nouns; documenting them inflates the catalog without proportionate reader value.
- CLAUDE.md's "Build & test" section already documents how to use the harness; `test/scenarios/` is grep-discoverable; the smoke fixture is referenced inline from rules.md.
- 46 concepts is well over the 15–25 heuristic; concepts whose body is structurally a test-hygiene checklist are defensible dropouts.

## Resolution candidates (do NOT pick)

- **Drop** `concepts/scenario-harness.md` from the catalog. Update any `Adjacent: scenario-harness` references (currently only `concepts/conformance.md`) to reword inline ("in-repo scenario tests under `test/scenarios/` use `modeling/scenario.Start` as their bring-up") or remove. Leave CLAUDE.md "Build & test" as the documented home.
- **Fold** into `conformance` as a "Scenario harness" subsection. Net -1 concept. Less clean than drop because the audiences differ.
- **Keep standalone** (status quo).

(Pre-decided shape: drop.)

## Evidence

- `concepts/scenario-harness.md`.
- `concepts/conformance.md` Adjacent block.
- CLAUDE.md "Build & test" section.
- `test/scenarios/`, `test/smoke/setup.go`.
- `review-notes.md` "Possible merges / splits to reconsider" / `scenario-harness` bullet.

