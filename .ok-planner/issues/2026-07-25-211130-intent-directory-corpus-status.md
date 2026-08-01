---
issue: intent-directory-corpus-status
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# design/intent/ is authoritative in content but illegal in form — archive only after the corpus absorbs it

`design/intent/` holds 77 per-concept dossiers distilled on 2026-07-13 from session transcripts for a drift-remediation campaign — written deliberately from the recorded design conversations, not from the then-current code. The campaign completed and its results are archived, but the directory stayed under `design/`, where the planner recognizes exactly three live catalog kinds (concepts, stories, decisions) plus one named point-in-time exception. The owner has since stated the fact that changes what resolving this means: the dossiers are redundant with stories and decisions *in principle* — those catalogs are meant to be the authoritative surface — but the dossiers are currently *more accurate and complete in places*, because the corpus drift that motivated the campaign is what made them necessary. Archiving the folder as a stale record would strand authoritative content; declaring it live corpus isn't available, because the planner's model admits no fourth kind.

The one live code reference makes the content gap concrete: a fitness test cites `design/intent/claim-scope.md` in its failure message as the source of canonical naming symbols (`code:test/plumbline/claim_scope_naming_test.go`), and the live `concept:claim-scope` does *not* carry those symbols — so even the narrow "repoint the test" fix requires folding content first. Meanwhile the dossiers have begun to rot at the edges (two still describe an import exemption the corpus has since eliminated), so the reconciliation's value decays with time: every week the dossiers age, distinguishing "intent is right, corpus lags" from "intent is stale, corpus moved on" gets harder.

## Options

- **Reconcile, then archive** — a dedicated sprint (or a batched sequence) walks each dossier against its live concept/story/decision counterparts, folds in what intent has that the corpus lacks or contradicts, files what needs judgment as issues, then moves `intent/` to `history/` — folding the claim-scope naming symbols into `concept:claim-scope` and repointing the citing test on the way. Real work, sized like the drift-remediation campaign that produced the dossiers.
- **Archive now, reconcile never** — cheapest; permanently strands whatever the dossiers hold that the corpus lacks, directly against the owner's stated constraint.
- **Leave in place** — continued rot, continued misleading citations, and the reconciliation only gets harder.

The ruling decides the reconciliation's shape and priority; the archive itself is forced by the three-kind model.

## Ruling

> Generated ruling (/verify-issues): a sprint reconciles the 77
> dossiers into the live corpus and then archives design/intent/ to
> history/ — per dossier, fold what intent holds that the live
> concepts, stories, and decisions lack or contradict (filing
> genuine judgment calls as intake issues rather than deciding them
> in the pass), and on the way fold the claim-scope naming symbols
> into concept:claim-scope and repoint the citing fitness test. The
> planner's three-kind model forces the archive; the owner's
> authoritative-in-content constraint forces reconciliation first;
> the dossiers' ongoing rot argues for scheduling it soon rather
> than eventually.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
