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

## Mechanical Checks

- Every written constraint needs a check that fails on violation: layering → dependency lint, invariant → assertion + test, wire contract → conformance suite, boundary shape → type
- Lint config is authoritative: if prose and lint disagree, lint wins

## Comments

- Code should be self-explanatory. The rule is: **no comments in source files**, with three narrow exemptions.
- **Machine directives** are allowed: license headers (`SPDX-License-Identifier:`, `Copyright`, `Licensed under`, `Dual-licensed`), lint suppressions (`eslint-disable`, `ts-ignore` / `ts-expect-error` / `ts-nocheck`, `noqa`, `pylint:`, `nolint`, `biome-`, `prettier-`, `tslint:`, `deno-`), build tags (`go:`), generated-file markers, C-pragmas, shebangs.
- **Configured citation tags** are allowed in slug-only form: tags declared in `.plumbline.json`'s `citations` array, where each entry pairs a tag with a structural resolution rule. Each line in a citation comment is `// @<tag>: <slug>` and nothing else — no em-dash tail, no continuation prose, no trailing punctuation. The slug names the design artifact; the artifact holds the explanation. Multiple clean citation lines may stack as one block (e.g. `// @concept: cascade` immediately followed by `// @story: parker`). Each line's slug is independently checked for resolution. Plumbline ships zero default citation tags — projects declare them.
- **Documentation comments** are allowed only in files carrying the opt-in marker `// @plumbline:allow-docstrings` (or `# @plumbline:allow-docstrings`). With the marker, JSDoc-style block comments on TS/JS declarations and GoDoc-style line comments on Go declarations are exempt. Without the marker, they are not.
- Everything else is residue. The default action for any other comment is **delete**. Load-bearing information — a constraint, an invariant, an intentional choice — belongs in a name, a type, an assertion with a message, or a test. Comments are the wrong layer for any of it.

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

- `plumbline <path>` — the lint binary; runs two checks: `comment-hygiene` (the rule above) and `citation-resolution` (every configured citation's slug must resolve). Exit 0 clean, 2 violations, 1 internal error.
- `/plumbline:affirm` — install or refresh `.claude/rules/plumbline-cheatsheet.md` from the plugin's canonical version.
- `/plumbline:audit` — run the lint over the whole project and group findings into a remediation plan.
- A `PostToolUse` hook auto-runs the lint on every Edit/Write; violations block (exit 2) so the agent sees them and fixes in the same turn.
- Project config lives in `.plumbline.json` at the repo root (optional). The `citations` array adds project-specific structured-tag exemptions (each pairs a tag with a resolution rule); `ignore` adds paths to skip.
