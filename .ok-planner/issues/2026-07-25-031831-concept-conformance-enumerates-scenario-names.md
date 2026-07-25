---
issue: concept-conformance-enumerates-scenario-names
kind: audit
category: other
artifacts:
  - concept:conformance
status: verified
opened: 2026-07-25T03:18:31Z
---

# The conformance concept reads like the test suite's table of contents

Rimsky ships a conformance kit — CLI subcommands that exercise a third-party implementation of one of its protocols (a custom executor doing dispatched work, a custom claim producer handing out data leases) against a standard battery of checks. Its concept document is supposed to define what the kit *is*; the house rule forbids concepts enumerating their own instances. Instead, the document names essentially every individual test scenario per protocol — for executors alone: happy path, async handoff, restart survival, cancel, attribute serialization, tags round-trip, park-and-resume, park emission, malformed input — with similar lists for the other protocols, several scenarios getting a full sentence each. It's one of the longest files in the catalog, and it contradicts its own Boundaries section, which claims the concept owns "the library and handler structure," not a scenario list.

Not all of the detail is trivia, which is what makes the trim non-mechanical. A few passages state genuine behavioral commitments — the policy that escalates to a hard failure when a stub-mode requirement isn't met, the raw-wire fallback probe that catches an implementation fabricating results — and those already *partly duplicate* into the document's own Invariants section, so the file currently says them twice at two altitudes.

## Options

- **Full compression** to a shape-level description, the scenario inventory living in code alone — the largest trim; the load-bearing policies need a surviving home.
- **Relocate scenario descriptions into decision documents** (which may name specifics) — preserves the reasoning at the licensed altitude, at the cost of new files per battery.
- **Partial trim**: keep per-protocol counts and categories, consolidate the genuine policies into Invariants only (killing the duplication), drop the per-scenario prose.

The ruling decides which detail is commitment versus inventory, and which of the three shapes the file takes.

## Ruling

> Recommended ruling (/recommend-rulings): Partial trim: shape-level
> description with per-protocol scenario counts and categories; the
> load-bearing policies (require-stub-mode escalation, the raw-wire
> fallback probe) move exclusively into Invariants, removing the
> current duplication. No new decision files.
>
> Rationale: The scenario inventory is code-owned membership; the
> policies are genuine commitments that already half-live in
> Invariants — consolidating there keeps them provable without
> inventing decision files per battery.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
