# Cold Read Cheatsheet

Actionable conventions for this codebase. Full docs: `cold-read/cold-read-style-guide.md` and `cold-read/cold-read-manifesto.md`.

## File Organization

- One feature per file, organized by feature not layer
- Max 2 levels of directory nesting
- Tests co-located as `*_test.ts` next to source
- ~500 line file guideline, ~100 line function guideline
- Max 3 levels of nesting depth (use early returns)

## Dependencies

- Max 2 files of import depth to understand any feature
- Import shared types from `shared/`, not between features
- No base classes, abstract factories, DI containers, or "Manager" classes

## Explicit Code

- Explicit parameters over dependency injection
- No behavior-modifying decorators (`@retry`, `@cached`, etc.)
- No convention-based magic or implicit middleware
- Configuration as visible objects, not scattered env lookups

## Duplication Tracking

- Prefer tracked duplication over hidden coupling
- Use `@source: path/to/file.ts:functionName` on copies
- Mark intentional divergence: `@diverged: true` + `@reason:`
- Extract to shared only when: 3+ call sites, identical logic, stable, <50 lines

## Shared Code Contracts

- `@agent-contract` blocks on shared infrastructure (5+ importers)
- `@blessed-invariant` on stable cross-cutting concerns
- Contracts answer: what, how to use, what it handles, what it doesn't, thread-safety

## Types

- Required at boundaries: API inputs/outputs, DB models, feature interfaces, config
- Flexible internally

## Change Propagation

- When modifying canonical source: search `@source:` refs, update or mark diverged
- Update `feature-index.md` when features/dependencies change
