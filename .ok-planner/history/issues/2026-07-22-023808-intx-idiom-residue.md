---
issue: intx-idiom-residue
kind: human
category: uniformity
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-22T02:38:08Z
---

# A retired naming pattern still lives in two spots — and twenty other names look like it without being it

Rimsky's persistence layer (the code that talks to Postgres and SQLite) once used a paired naming pattern: a public method `X` that could open its own database transaction, backed by a private helper `XInTx` that ran inside an existing one. A cleanup retired that pattern, collapsing each pair into one method taking an optional transaction parameter. Two spots survived unswept — the API-key revocation and deployment-CA code, in both database backends — and they're a hazard beyond aesthetics: a live pair reads as the house pattern, so the next contributor extending those files copies the retired idiom. Meanwhile about twenty *single* functions (no public twin at all) carry the bare `InTx` suffix, which may not be residue at all — a suffix with no pair plausibly means something different: "this helper requires an already-open transaction," full stop.

Re-verification sharpened the picture. The cleanup commit's own message enumerates exactly what it collapsed and explicitly blesses one apparent lookalike — the blob backend's two-interface capability split (for storing large binary data) — as intentional, "a capability split, not a duplicated pair." The two live pairs were simply never touched. And none of this convention is written down anywhere durable: it exists in a commit message and a closed audit row, never in the design corpus. The only standing rule is the project-wide "no two dialects for one job" — which demands finishing the sweep but doesn't say whether "requires a transaction" and "optionally opens one" are one job or two.

## Options

- **Collapse the two remaining pairs.** Mechanical, proven transform; removes the copy-source hazard. Hard to argue against given everything around them was already converted.
- **The twenty bare-suffix singletons**: rename them to match the sweep — or rule the no-twin suffix a *different, legitimate* convention ("mandatory open transaction") and write it down so it stops getting re-flagged as drift.
- **The blob interface's `WriteInTx`/`ReadInTx`**: keep as named (the split is already blessed; only the spelling echoes the old idiom), rename to kill the echo, or fold the interfaces — the last being a real design change beyond a naming fix.
- **Document-only close** — leaves the two copy-source pairs standing, the weakest position given the sweep fixed everything else.

The ruling decides: finish the pairs; residue-or-convention for the bare suffix; keep/rename/fold for the blob methods.

## Ruling

> Recommended ruling (/recommend-rulings): Collapse the api-key and
> deployment-CA X/XInTx pairs (finishing the fa8b24f4 sweep). Bless
> the single-form ...InTx suffix as the 'requires an open transaction'
> convention and state it in a small decision so it stops being re-
> flagged. Keep TxBlobBackend's WriteInTx/ReadInTx as named.
>
> Rationale: The pairs are unswept residue of exactly the idiom the
> sweep retired — same job, second dialect. The single-form suffix is
> a different job (mandatory tx, not optional-tx pairing), legitimate
> under one-idiom-per-job once it's written down.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
