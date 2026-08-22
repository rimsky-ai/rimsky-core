---
concept: claim-scope
---

# Claim Scope

## What it is

A claim scope is the opaque byte stream that says what a claim acquired. The producer returns a claim scope when it grants a claim, or supplies one per sub-claim when it splits a scope for fan-out, and rimsky persists it on the claim-handle ledger (see `concept:claim-producer`, `concept:claim-handle`). A selector and a claim scope are the two ends of one resolution. The graph author writes the selector, the opaque text in a node's claim declaration; a selector may carry unresolved substitution directives while the author is writing the template, and those directives resolve at dispatch. The producer then parses the resolved selector in its own language and answers with canonical bytes: the resolved selector itself, or, where the producer picks among candidates rather than matching what the author wrote, the identifier of what it picked. A claim scope is always post-resolution, and it never carries a substitution directive. Where a producer reports no scope at all, the persisted scope is rimsky's own rendering of the resolved selector, so the record stays meaningful rather than blank.

## Purpose

Rimsky has to tell whether two claims target the same data, across producers whose domains it knows nothing about, and the claim scope is what makes that possible. The producer folds its domain knowledge into the bytes and rimsky compares the bytes. A producer whose overlap semantics are richer than a comparison can express answers the overlap question itself instead (see `decision:byte-equal-conflict-default`, `concept:claim-producer`).

## Boundaries

Claim scope owns the comparison rimsky makes when it checks for a conflict, and the discipline that leaves the bytes unread everywhere else (see `concept:inertness`). It does not own canonicalization, which is the producer's job; capacity counting, which belongs to `named-lock`; or the other opaque streams a claim carries, its address and its payload, which belong to `claim`. Claim scope is not run scope. The two share a word and name different things: a claim scope identifies what a claim acquired, and a run scope identifies which instantiation of a graph a run belongs to (see `concept:run-scope`). See also: `claim`, `claim-handle`, `claim-producer`, `write-semantics`, `inertness`, `run-scope`.
