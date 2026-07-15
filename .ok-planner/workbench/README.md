# workbench — active, interactively-driven implementation docs

This folder holds **operating documents for efforts being implemented
interactively** — a hand-authored plan plus its live tracking/strategy notes —
that are not `/write-plan` output tied to a spec.

## Context rule (the inverse of the other `.ok-planner/` folders)

`specs/`, `plans/`, `sketches/`, and `history/` are **out-of-context by
default** — you don't pull them in unprompted. `workbench/` is the opposite:
**a document here is in-context WHILE the effort is active** — it is the
driving document for the work in flight, meant to be read and followed.

When an effort completes, its workbench document is archived to `history/`
(at which point it becomes an out-of-context past record like anything else).

## Relation to the findings ledger

A workbench plan carries **strategy and sequencing** — phases, ordering,
fleet mechanics, discovered follow-ups' provenance. Concrete **issues** live
in the work ledger they drive from (for the current effort,
`review-findings-2026-07-06.csv`), not here. The plan points at ledger ids;
it does not duplicate their tracking.

## Status

Not yet a sanctioned ok-planner concept — this is a local convention. Making
it first-class (created and documented by `affirm`, with these semantics
written into `.ok-planner/CLAUDE.md`) is a pending change to the ok-planner
plugin.
