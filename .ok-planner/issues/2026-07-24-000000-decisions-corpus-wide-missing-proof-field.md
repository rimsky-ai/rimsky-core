---
issue: decisions-corpus-wide-missing-proof-field
kind: audit
category: proof
artifacts:
  - design/decisions/*
status: verified
opened: 2026-07-24T00:00:00Z
---

# 232 of 236 decisions have no stated way to catch their own violation

Rimsky's design corpus requires every decision document to carry a Proof section: the concrete, mechanical check — a lint rule, a test, a config assertion — that fails if the choice is silently violated. The rule's teeth: a decision nobody can name a violation-check for is either really a default (delete it) or an unenforced intention (file it as an open question) — not catalog material. A whole-corpus review found nearly the entire catalog non-compliant: 232 of 236 live decisions have no Proof section. Only a manual review catches this; no lint does. The question isn't what any individual Proof says — it's how to run a 232-file remediation.

Sampling shows three very different difficulty tiers hiding in the count. Some decisions already have an enforcing check in the codebase (import-boundary lint rules, the license checker, pinned library versions) and just need the section written and pointed at it — nearly free. Others state rules enforced nowhere — a library choice with nothing stopping a rival import — where a Proof means designing a brand-new check: real engineering, not documentation. A third tier probably aren't decisions at all and should retire as defaults. Three sibling issues interlock: the same files missing their Alternatives sections (same per-file read, same tradeoff reconstruction), ~160 decisions with no code citation (a citation only exists once a check is named), and one single-decision case already filed separately.

## Options

- **Phase by enforcement status**: retire the defaults first, then the near-free "check already exists" tier, then handle the needs-new-tooling tier — with new check design either in scope or split out as filed unenforced-intentions.
- **Phase by topic category** (persistence, protocol, build) — reviewable batches, fuzzy boundaries, and the Alternatives sweep needs matching batches.
- **One bulk sweep** — catches cross-file duplication in one pass; review fatigue over 232 files is the real risk.
- **Joint or separate** from the Alternatives sweep and the two citation issues — one read per file versus keeping independently-ruled issues independent.

The ruling decides: the phasing axis; joint-vs-separate with the siblings; whether authoring brand-new checks is in scope; and whether retirement runs first.

## Ruling

> Recommended ruling (/recommend-rulings): Phase by enforcement
> status, jointly with the Alternatives sweep and absorbing
> issue:decision-blob-backend-missing-proof-field and the coverage-
> gap-decisions scope: pass 1 retires defaults-with-no-alternative;
> pass 2 adds Proof + annotation where the check already exists
> (depguard, license-lint, pins); decisions needing net-new tooling
> are filed as unenforced-intention issues per DECISION-DEFINITION's
> fallback rather than blocking the sweep.
>
> Rationale: The retire-first ordering shrinks the 232 before anything
> is authored, the exists-already bucket is near-free, and new check
> design is real engineering that shouldn't hide inside a
> documentation sweep.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
