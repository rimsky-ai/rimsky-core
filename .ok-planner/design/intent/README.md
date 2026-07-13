# Design-Intent Ledger

One dossier per concept, distilled on 2026-07-13 from the project's full recoverable design history, built to adjudicate the drift-remediation findings ledger (`review-findings-2026-07-06.csv`).

## Provenance

Two evidence tiers, in strict precedence order:

1. **transcript** — the project owner's own words in Claude Code session dialogue, 2026-06-12 .. 2026-07-13 (44 sessions; earlier sessions were lost to transcript retention). Ground truth.
2. **artifact** — ok-planner history specs, plans, plan-notes, and plan-divergences, 2026-05-04 .. 2026-06-11. Agent-written and only user-reviewed; specs are known to sometimes canonize inaccuracies. Claims resting solely on this tier are flagged `(artifact-only)`.

1,959 distilled intent entries (decisions, constraints, feature-promises, corrections, rejections, reversals) were consolidated per concept under two rules: later intent supersedes earlier, and transcript outranks artifact.

## Dossier structure

- **Net position** — what must be true today.
- **Required behaviors (open promises)** — promised and never retracted; each must exist in code and be test-guarded.
- **Intentional absences** — retired/rejected features whose absence is by design; never "restore" these.
- **Corrections and restorations** — documented drift incidents and rulings; adjudication precedents.
- **Superseded / historical** — earlier positions later reversed.
- **Conflicts needing human ruling** — contradictions the record does not resolve.

Citations are `(YYYY-MM-DD, <session-id-or-artifact-stem>, <tier>)`; raw entries live in the working extract (`tmp/intent-extract/`, uncommitted), and the raw transcript snapshot is preserved at `tmp/intent-extract/raw-transcripts-snapshot-2026-07-13.tar.gz`.

## How to use in adjudication

For a finding on concept X, read X's dossier and rule:

- behavior required by the dossier, absent/broken in code → **fix-code** (or **restore-feature** when the finding is a symptom of a missing capability)
- code matches dossier, doc/finding assumes a superseded position → **fix-doc** / **refute**
- the thing the finding wants is an intentional absence → **refute** (and never resurrect it)
- the dossier itself lists it under conflicts → escalate for a human ruling

These dossiers record intent history only. They were deliberately written without consulting current code or the concept docs, so they can be compared against both without circularity. Where a dossier and a concept doc disagree, the disagreement is itself a finding to adjudicate — the dossier does not automatically win, but the transcript quotes inside it do.
