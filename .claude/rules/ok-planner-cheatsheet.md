# ok-planner Cheatsheet

Materialized by ok-planner v4.3.1. Plugin-owned: overwritten
wholesale by `/true-up`; project-specific rules belong in your own files under
`.claude/rules/`.

The planner's estate lives in `.ok-planner/` (its embedded `CLAUDE.md` carries
the full per-directory rules). The short version every session needs:

## The three content kinds

- **`design/` — source of truth, read freely.** Concepts, stories, decisions:
  the project's durable model, same weight as code. Changed only by applying
  an approved sprint spec's corpus deltas — never ad hoc. Code cites it via
  `@concept:` / `@story:` / `@decision:` annotations.
- **`issues.jsonl` — the human-review backlog.** Append-only JSONL event log;
  an issue's state is the fold of its rows by id. Anyone may append `open`
  rows; only a `/sprint` session resolves. Never edit or delete rows.
- **`specs/`, `sketches/`, `history/` — records, out of context by default.**
  Do not read them to understand the project, do not include them in general
  exploration, do not reconcile them with current code. `sketches/` is
  pre-spec future thinking (written by `/sketch`); `history/` is the archive —
  same-named folder per artifact kind, preserved indefinitely. Touch records
  only when the user or an ok-planner skill directs it.

## Lifecycle

`/sketch` captures an idea in `sketches/` (no authorization to build).
`/sprint` drains the issue queue with the owner, then produces a spec in
`specs/`. An implementation orchestrator executes the spec: applies corpus
deltas to `design/`, builds, `/prove`s every touched story and decision, and
finishes with `/audit`, whose judgment findings land back in `issues.jsonl`.
On completion, artifacts move to their same-named folder under `history/`.

## Hard rules

- While any issue is open, `/sprint` plans no new work — the queue drains first.
- Design docs are current-state only: no changelogs, no roadmaps, no TODOs.
- Nothing in the suite runs true-up from a hook; it is always a user action.
