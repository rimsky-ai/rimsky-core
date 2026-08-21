---
issue: blob-backend-mismatch-read-errors
kind: audit
category: conflicting
artifacts:
  - concept:blob-backend
status: promoted
sprint: 2026-08-21-intake-drain-and-concept-repair.md
opened: 2026-08-16T08:47:35Z
---

# Reading a spilled attribute under a different blob backend errors, but the concept promises a fall-back

Rimsky spills large attribute values to a blob backend and keeps a handle on the row. The blob-backend concept promises that a read of a row whose handle names a backend other than the active one falls back to the inline data column instead of erroring, so continuity survives a backend switch. Three of the four spilled-read paths break that promise: both attribute-row readers and the runtime scratch loader return an error on a mismatch and never read the inline column. Only the carry-forward path falls back, and it delivers no continuity either, because a spilled row's inline column holds an empty object and the fall-back substitutes an empty bag rather than the prior value. The ruling decides the policy and fixes the text to it. The policy is error, degrade, or split by call site.

The trigger is ordinary. An operator changes the configured blob backend while old rows still carry handles from the previous one. A receiver's substitution and a dispatch's scratch load then return an error on such a row. Carry-forward proceeds with nothing and reports no failure.

## Options

- Rewrite the invariant to match the code (reads and scratch loads error, carry-forward degrades) and fix the misleading empty-bag fall-back; cost: a backend switch stays a hard boundary for old rows.
- Make the erroring paths degrade to the inline column; cost: substitutes an empty value on the execution path without warning, and masks real loss.
- Split the rule by call site, so a current-run read errors and a historical copy degrades, and make the degrade honest; cost: two behaviours to state and test.

The ruling decides what a backend mismatch means for the reader.

## Ruling

> Recommended ruling (/verify-issues): Make the mismatch an error everywhere, including carry-forward, which now degrades to an empty bag. Rewrite the invariant to say so: a read refuses a spilled row written under another blob backend until the operator migrates the rows or restores the old backend.
>
> Rationale: a clear failure beats handing an executor or a downstream node an empty attribute where a value belongs. Three of the four paths already error, and the one degrading path never delivered continuity. Flip case: if a backend migration tool lands that rewrites handles, the fall-back becomes moot and the invariant can promise continuity through migration instead.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
