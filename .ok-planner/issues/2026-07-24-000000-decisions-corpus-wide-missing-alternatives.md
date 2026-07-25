---
issue: decisions-corpus-wide-missing-alternatives
kind: audit
category: proof
artifacts:
  - design/decisions/*
status: verified
opened: 2026-07-24T00:00:00Z
---

# 134 "decisions" never say what they decided against

In rimsky's design corpus, a decision document records a real technical choice — and the rule that keeps the catalog honest is its mandatory Alternatives section: the option genuinely considered and rejected. A choice with no real alternative isn't a decision, it's a default, and defaults are supposed to be retired from the catalog, not documented. A whole-corpus review found roughly 134 of ~236 live decisions missing the section entirely. Some are library picks where the rejected option is obvious and just needs writing (a different logger, a CLI framework); others are probably genuine defaults that should be deleted. At 134 files this is a project to schedule, not a fix to make — this issue is about how to run it.

Two facts shape the scheduling. No automated check catches a missing Alternatives section — only manual review found this, so the gap can regrow. And a sibling issue from the same review covers an even larger gap in the same files: 232 decisions missing their mandatory Proof section (the check that fails if the choice is violated). Judging "was there a real alternative?" and "what would a violation look like?" both require reconstructing the same tradeoff from the same file — a strong argument for one combined pass rather than reading 134 files twice.

## Options

- **Per-file judgment, jointly with the Proof sweep**: author real Alternatives where an option existed, retire the defaults; one read per file covers both gaps. A file missing *both* sections is the strongest retirement candidate.
- **Bulk-author Alternatives for all 134** — fast, and risks manufacturing plausible-sounding filler that defeats the section's purpose.
- **Phase by topic area** (persistence, protocol, build…) — a scheduling choice combinable with either of the above.
- **Loosen the rule** — make Alternatives optional rather than clearing the backlog; changes what "compliant" means instead of reaching it.

The ruling decides: judgment or bulk; joint with the Proof sweep or separate; the phasing axis; and who exercises the per-file judgment (agent pass with owner spot-checks, or full owner walkthrough).

## Ruling

> Recommended ruling (/recommend-rulings): Run jointly with the
> missing-Proof sweep as one per-file pass: author real Alternatives
> where a genuine option existed, retire as defaults where none did.
> Agent pass with owner spot-check, not per-file owner walk-through.
>
> Rationale: Both gaps require the same read and the same real-choice-
> vs-default judgment; one pass per file halves the work, and a
> decision missing both sections is the strongest retirement signal.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
