---
tension: event-log-split-into-two
category: muddy-boundary
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - event-log
  - named-event
  - observability
supersedes:
  - events-table-name-overlap
resolution:
  shape: slim-source-and-fold-ledger
  slimmed: concepts/event-log.md (audit-log-only; filename retained)
  folded-into: concepts/named-event.md (Ledger storage subsection)
  supersedes: events-table-name-overlap
  summary: |
    Folded the rimsky_node_events named-event ledger into
    concepts/named-event.md as a "Ledger storage" subsection. Slimmed
    concepts/event-log.md to cover only the rimsky_events audit log
    (filename and slug retained per refine-design step 5, option C).
    events-table-name-overlap is automatically resolved by this split.
---

# `event-log` bundles two tables with different consumers, schemas, and opacity disciplines

## What is muddy

`concepts/event-log.md` covers both `rimsky_events` (rimsky-internal audit log, free-form `kind`, rimsky-readable JSONB, drives `/events` dashboard) and `rimsky_node_events` (executor-emitted named-event ledger, inert payload per the named-event inertness rule, read by attribute substitution and `on_event` handlers). The concept doc structurally separates them inside one file, but the unifying frame ("append-only events tables") is weak: different consumers, different schemas (`kind` free-form vs. `(emitter, event_name, seq)`), different opacity disciplines, different protocol-surface position (audit log is rimsky-internal; ledger is executor-protocol-facing).

`tensions/events-table-name-overlap.md` already catalogs the naming overlap as a separate concern. The split-or-merge question is the structural decision behind it.

## Why it matters

A reader hitting `event-log.md` learns the two tables exist but has to triangulate which concept *each* belongs to functionally. The named-event ledger is structurally part of `named-event` (which already exists); the audit log is structurally part of either `observability` (it feeds the cascade-graph endpoint) or its own small concept. Keeping them merged forces the catalog to maintain a synthetic noun for a category that does not exist at runtime.

## Resolution candidates (do NOT pick)

- **Fold the named-event ledger into `named-event`** as a "Ledger storage" subsection, **and create a new `audit-log` concept** for `rimsky_events`. Two narrow concepts replace one wide one; concept count unchanged.
- **Fold the named-event ledger into `named-event`**, **and fold the audit log into `observability`** as an "Audit log" subsection (`/events` dashboard already lives there). Concept count drops by one.
- **Fold the named-event ledger into `named-event`**, **and keep `event-log` as the audit-log-only concept** with the named-event content removed. Equivalent to the first option modulo naming.

When we walk this, the sub-decision is the placement of the audit log: standalone `audit-log` concept, subsection of `observability`, or rename `event-log` → narrow audit-log concept.

**Picked shape (refine-design step 5):** Fold the named-event ledger into `concepts/named-event.md` as a "Ledger storage" subsection, **and slim `concepts/event-log.md` to audit-log-only** (keep the existing filename and slug; remove the named-event-ledger half; the file becomes the canonical home of the `rimsky_events` audit log). Update any Adjacent references that pointed at `event-log` for ledger-related reasons to point at `named-event` instead. The `tensions/events-table-name-overlap.md` tension is superseded by this resolution.

## Evidence

- `concepts/event-log.md`.
- `concepts/named-event.md`.
- `concepts/observability.md` (`/events` dashboard / cascade-graph endpoint).
- `tensions/events-table-name-overlap.md`.
- `review-notes.md` "Judgment calls" / `event-log` bullet.

