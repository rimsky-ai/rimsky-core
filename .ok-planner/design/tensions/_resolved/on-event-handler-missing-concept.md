---
tension: on-event-handler-missing-concept
category: unspecified
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - lifecycle-handler
  - named-event
  - node
resolution:
  shape: promote-new-concept
  new-concept: concepts/on-event-handler.md
  summary: |
    Promoted on-event-handler to a concept with Definition (key-indexed
    map of {event_name → handler}), Purpose, Boundaries, Invariants
    (capabilities cross-check at template registration; unknown
    event names as no-ops if peer unreachable). Three dangling
    Adjacent slugs in lifecycle-handler.md, named-event.md, node.md
    are now valid cross-links to the new concept.
---

# `on-event-handler` is treated as a concept by cross-links but has no concept file

## What is muddy

Three concept files cite `Adjacent: on-event-handler` (`concepts/lifecycle-handler.md`, `concepts/named-event.md`, `concepts/node.md`), but `.ok-planner/design/concepts/on-event-handler.md` does not exist. The `on_event` handler map *is* structurally distinct from the four single-slot lifecycle handlers (key-indexed map of `{event_name → handler}` vs. a single resolver), shares the resolve+invalidate vocabulary, and has its own gate (per-event executor-capabilities validation at template registration). A reader following the cross-link expects a concept entry and finds nothing.

## Why it matters

- Cross-link integrity: three dangling `Adjacent:` slugs survived discover-design final approval.
- Catalog coverage: the `on_event` map is load-bearing for the reactive-loops design (`@blessed-invariant`-adjacent: `on_event` handlers gated against `Capabilities.declared_events`) and currently has no canonical noun entry.
- Defect surface: an agent reasoning about reactive policy reads `lifecycle-handler.md` and sees a one-line aside referring to a sibling concept that doesn't exist.

## Resolution candidates (do NOT pick)

- **Promote** to its own concept file at `concepts/on-event-handler.md`, with Definition / Purpose / Boundaries / Invariants (the map shape, the per-event resolver, the capabilities-cross-check at registration, the runtime-unknown-event-as-no-op behavior). Update the three citing concepts' `Adjacent:` lines to point at the new slug.
- **Fold** into `lifecycle-handler.md` as a subsection ("the `on_event` map: a key-indexed sibling"). Scrub the three `Adjacent: on-event-handler` lines and replace with prose-level references.

## Evidence

- `_discover/reactive-loops-and-lifecycle-handlers.md`.
- `concepts/lifecycle-handler.md` Adjacent line.
- `concepts/named-event.md` Adjacent line.
- `concepts/node.md` Adjacent line.
- `review-notes.md` "Suspected-but-unconfirmed concepts" / "Unresolved issues".

