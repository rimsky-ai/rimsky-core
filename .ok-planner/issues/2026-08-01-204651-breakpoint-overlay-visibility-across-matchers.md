---
issue: breakpoint-overlay-visibility-across-matchers
kind: human
category: conflicting
artifacts:
  - concept:breakpoint
status: verified
opened: 2026-08-01T20:46:51Z
---

# When several breakpoints pause one dispatch, does a later matcher see an earlier resume's edits?

Rimsky's debugger lets an operator set pause breakpoints on a node dispatch; on resume, the operator may attach a one-shot attribute overlay (the "L6" layer) that edits what the dispatch will run with. Several breakpoints can match the same dispatch, and they pause and resume strictly in sequence. The contested question: when hit N's resume applies an overlay, does breakpoint N+1's matcher — and the snapshot of the attribute bag shown to the operator at hit N+1 — see those edits, or the bag as it stood before any overlay?

The design-intent record from the original debugger work said isolation: matchers evaluate against a snapshot captured at evaluation entry, so matching is independent of resume-overlay contents. The code today deliberately does the opposite: a defect-sweep commit (`2627ae3c`, 2026-07-20) changed the evaluation loop so each resume overlay merges into the bag the next matcher and next hit snapshot read (`code:lib/runtime/breakpoint_eval.go`), and pinned that behavior with a test. The live `concept:breakpoint` is silent on which reading is the commitment — it only promises that an overlay never persists past the single dispatch that hit the breakpoint. So the corpus currently arbitrates neither, and either the sweep fixed a real bug or it quietly overwrote recorded intent.

The practical difference an operator sees: under the code's reading, a later breakpoint's matcher and snapshot reflect *what will actually dispatch* after earlier edits; under the isolation reading, matching is deterministic against the pre-overlay bag regardless of what earlier resumes injected — but the snapshot the operator inspects can then differ from what actually runs.

## Options

- Ratify the code: overlays are part of the effective bag from the moment they apply, so later matchers and snapshots observe them; write that invariant into the breakpoint concept. Cost: a breakpoint can match (or stop matching) because of what an operator typed at an earlier resume — matching is no longer independent of debug-session actions.
- Restore the recorded isolation contract: matchers and snapshots evaluate against the bag captured at evaluation entry; revert the evaluation change and its pinning test, and write the isolation invariant instead. Cost: undoes a deliberate defect-sweep fix, and the operator's snapshot at a later hit no longer shows what will really run.

The ruling decides which reading becomes the concept's invariant — and therefore which version of the code is correct.

## Ruling

> Recommended ruling (/verify-issues): Ratify the code — a resume
> overlay joins the dispatch's effective bag immediately, and later
> matchers and hit snapshots see it. Write that into the breakpoint
> concept as an invariant.
>
> Rationale: the debugger's core promise is that what the operator
> inspects at a pause is what will actually dispatch; the isolation
> reading breaks exactly that (a later snapshot would show a bag the
> dispatch will not run with), which is presumably why the defect
> sweep judged it a bug and pinned the opposite. Determinism of
> matching against operator edits is worth less than truthful
> snapshots — the operator making the edits is the same person
> watching the matches. Flip case: if a debugger UI or automation
> ever needs to predict the full set of breakpoints a dispatch will
> hit before any resume happens (e.g. batch-arming with a guaranteed
> hit list), that prediction is only sound under isolation, and the
> call reverses.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
