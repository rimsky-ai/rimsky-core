---
issue: keepalive-attributes-handlers-lack-concept-executor-citation
kind: human
category: citation-coverage
artifacts:
  - code:lib/runtime/keepalive.go
  - code:lib/runtime/attribute_writeback.go
  - concept:executor
status: verified
opened: 2026-07-24T00:00:00Z
---

# The code enforcing a security rule doesn't point back to the document that states it

Rimsky links code to its design corpus with citation comments, added sparingly — only at points that enforce something a design document states. Two HTTP handlers in the supervisor (the process that dispatches work to executors and receives their callbacks) enforce a security rule: the two *ongoing* callback channels an executor uses mid-dispatch — keepalive pings and incremental progress updates — require a per-dispatch secret token as a bearer credential, on top of whatever transport-level trust is configured. The rule is owned and stated by the executor concept document; the handlers cite only their own narrow decisions about route shapes. A reader of the bearer-check code today has no signpost back to the invariant it enforces — a gap that contributed to a real misdiagnosis (a bug report concluded the token scheme was a defect, because the obvious documents never connected).

Two details sharpen the placement question. The check itself is implemented once, in a small shared helper both handlers call — currently uncited. And the apparent precedent (a sibling callback file that does cite the executor concept three times) turns out to cite an unrelated part of that document, so it isn't the pattern it looks like. The project's citation rule cuts against decorative tags, so "just add it everywhere" isn't free.

## Options

- **Cite the concept on both handlers** — most visible; duplicates one citation across two files for one shared rule.
- **Cite it once, on the shared helper** — the citation sits exactly where the rule is enforced; a reader who never steps into the helper misses it.
- **Fix it in the docs instead** — have the narrow decisions (or the supervisor's own concept document, which has the identical gap) point at the executor concept; code stays uncited.
- **Do nothing** — defensible under citation-minimalism, and leaves the misdiagnosis trap armed.

The ruling decides: cite at all; where; and whether the supervisor document's matching prose gap is fixed in the same pass.

## Ruling

> Recommended ruling (/recommend-rulings): Cite @concept: executor
> once, on the shared authorizeCancelToken helper — the invariant's
> actual enforcement point — not on both handlers. Close the identical
> gap in concept:supervisor's prose in the same pass.
>
> Rationale: The citation belongs next to the load-bearing check, and
> one tag at the shared site avoids the duplication Plumbline's
> citation-minimalism warns against.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
