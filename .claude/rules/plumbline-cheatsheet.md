# Plumbline Cheatsheet

Actionable conventions for this codebase under the Plumbline methodology. The full reference (manifesto and style guide) ships with the Plumbline plugin. Core idea: comprehension is cheap, verification is not — make wrong edits fail mechanically.

## File Organization

- One feature per file, organized by feature not layer
- Tests co-located next to source
- ~500 line file guideline (edit/merge granularity, not readability), ~100 line function guideline
- Max 3 levels of nesting depth (use early returns)

## DRY and Abstraction

- Strict DRY: semantically identical logic lives in ONE place — never copy what must change together
- Shared code must resolve statically: named symbols, enumerable interface implementations, explicit composition — reachable by grep and types
- Forbidden: DI containers, reflection-driven dispatch, convention-based registration, behavior-modifying decorators, base classes / "Manager" abstractions
- Shared code carries a fast contract suite runnable in isolation; multiple implementations of one interface share a conformance suite
- Check speed is a placement criterion: prefer placements covered by fast isolated tests; "only testable end-to-end" is a design smell
- `@source:` copies allowed ONLY for semantically independent code where divergence is expected (e.g. parallel driver implementations of one contract); mark divergence `@diverged: true` + `@reason:`

## Mechanical Checks

- Every written constraint needs a check that fails on violation: layering → dependency lint, invariant → `@blessed-invariant` + test, wire contract → conformance suite, boundary shape → type
- Lint config is authoritative: if prose and lint disagree, lint wins
- A `@blessed-invariant` annotation without an exercising test is unfinished

## Comments

- Every comment begins with a structured tag: `@constraint:`, `@deliberate:`, `@agent-contract`, `@blessed-invariant:`, `@source:`/`@diverged:`/`@reason:`
- Projects may extend the vocabulary with their own structured tags (e.g. design-model citations); plain prose is never allowed
- Machine directives exempt (build tags, generate directives, lint suppressions, license headers)
- All other prose comments are generation residue — clean up after writing, delete on sight
- `@agent-contract` = guarantees + what it does NOT handle; no usage tutorials

## Uniformity

- One idiom per job, repo-wide; never introduce a second way to do something
- When improving an idiom, sweep the old one out everywhere in the same change — no coexisting dialects
- Prefer plain over clever; lint-enforce whatever uniformity can be

## Explicit Code

- Explicit parameters over dependency injection
- Explicit registration of routes/handlers/bindings — never path- or name-derived
- Configuration as visible objects, not scattered env lookups

## Types

- Required at boundaries: API inputs/outputs, DB models, feature interfaces, config
- Flexible internally

## Repo-Wide Changes

- Shared-code change: edit the one definition, let compiler + contract suites enumerate blast radius, fix all consumers in the same change
- Idiom change: sweep all instances in the same change, add lint so the old idiom cannot return
- Update `feature-index.md` when features/dependencies change

## Tooling

The Plumbline plugin ships:

- `plumbline <path>` — the lint binary; checks comment hygiene, `@source:` validity, and `@blessed-invariant:` test coverage. Exit 0 clean, 2 violations, 1 internal error.
- `/plumbline:affirm` — install or refresh `.claude/rules/plumbline-cheatsheet.md` from the plugin's canonical version.
- `/plumbline:audit` — run the lint over the whole project and group findings into a remediation plan.
- A `PostToolUse` hook auto-runs the lint on every Edit/Write; violations block (exit 2) so the agent sees them and fixes in the same turn.
- Project config lives in `.plumbline.json` at the repo root (optional); `tags_extend` adds project-specific tags (e.g. `@concept:`), `ignore` adds paths to skip.
