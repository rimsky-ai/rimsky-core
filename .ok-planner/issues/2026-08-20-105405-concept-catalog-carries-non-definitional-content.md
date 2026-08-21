---
issue: concept-catalog-carries-non-definitional-content
kind: human
category: conflicting
artifacts:
  - concept:transition-reason
  - concept:rimsky
  - concept:node-run
  - concept:publisher-subscription
status: verified
opened: 2026-08-20T10:54:05Z
---

# The concept catalog carries content the authoring rules assign to code, decisions, and stories

48 of the 74 concept files violate the concept authoring rules. The rules say a concept names what kind of thing exists and does not list current instances — verbs, libraries, routes, wire identifiers — and that interface designs, route shapes, CLI grammars, schemas, and implementation diagrams live in code, not in `design/`. A five-reader sweep on 2026-08-20 found instance enumerations in about 23 files, interface or schema content in about 22, decision-style choice-arguments in 10, and story-style guarantees in 2. The full per-file findings are recorded in the workbench at `workbench/2026-08-20-concept-catalog-alignment-findings.md`. Examples: `concept:transition-reason` enumerates reason literals directly under its own sentence that membership is owned by the state-machine code; `concept:rimsky` enumerates CLI verbs and flag grammar; `concept:node-run` carries a seven-state machine table; `concept:publisher-subscription` states a guarantee that restates `decision:secret-at-rest-posture`.

The mechanism that let this accumulate: the sprint sign-off compliance reviewer transcludes the story definition and the decision definition but not the concept definition, so every sprint's concept deltas were checked for self-containment and truth of repository claims, never for definitional form. The one instrument that carries the concept definition is the periodic audit's worker, whose findings route to the intake, not the sign-off gate.

The catalog's roughly 505 invariants are in scope. The concept template sanctions the Invariants section, so those lines are compliant authoring under the current suite — but the owner holds that a concept is abstract: it neither requires nor forbids implementation behavior, and the Invariants sections are prescription by construction. The next ok-planner version removes the section from the concept template; this catalog goes first.

## Options

- Repair sweep: one sprint's corpus deltas over the 48 files, deleting misfiled content and moving what survives to its right kind; cost: a large single sprint touching most of the catalog.
- Per-file issues: file each concept's findings separately for individual rulings; cost: 48 issues that all turn on one rule already decided.
- Wait for the periodic audit: let the next `/audit` compliance pass record the violations; cost: the drift stays live and every new sprint adds to it.

## Ruling

> Repair sweep. Draft one sprint's corpus deltas over the 48 files from the workbench findings. The reader test for what survives in each file: a sentence stays only where a reader meeting the noun in code needs it to read the code — definitions, purpose, and boundaries stay; verb lists, wire literals, schemas, route shapes, and grammars are deleted, because the code and protos already carry them. Each choice-argument moves into the decision that owns it or becomes a new decision; each guarantee becomes a story or dissolves into the decision that already carries it. Invariants sections go: a concept is abstract and holds no prescription. Each entry takes the same reader test — the sweep deletes an entry a test or the code already enforces, because the enforcement site carries it; an entry arguing a choice moves into the decision that owns it or becomes one; an entry promising a user outcome becomes a story or dissolves into the one that carries it. The sprint surfaces a property with no enforcement site as a gap; it never keeps one silently as prose. Repointing what cites the sections — the CLAUDE.md safety-property pointer, the `invariant:` prose-citation kind — rides the same sprint. Ruled live by the owner, 2026-08-20 (widened the same day: the owner also rules the Invariants sections out, ahead of the matching template change in the next ok-planner version).
