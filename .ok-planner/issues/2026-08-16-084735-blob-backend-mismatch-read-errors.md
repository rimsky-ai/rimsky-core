---
issue: blob-backend-mismatch-read-errors
kind: audit
category: conflicting
artifacts:
  - concept:blob-backend
status: verified
opened: 2026-08-16T08:47:35Z
---

# Reading a spilled attribute under a different blob backend errors, where the concept promises a fall-back

Large attribute values are spilled to a blob backend and the row keeps a handle. The blob-backend concept promises that reading a row whose handle names a different backend than the active one falls back to the inline data column rather than erroring, so continuity survives a backend switch. Three of the four spilled-read paths — both attribute-row readers and the runtime scratch loader — return an error on mismatch and never consult the inline column; only the carry-forward path falls back, and even that path does not deliver continuity: a spilled row's inline column is written as an empty object, so the fall-back substitutes an empty bag, not the prior value. The ruling decides the policy — error, degrade, or split by call site — and fixes the text to it.

The trigger is ordinary: an operator changes the configured blob backend while old rows still carry handles from the previous one. Today a receiver's substitution and a dispatch's scratch load hard-fail on such a row; carry-forward silently proceeds with nothing.

## Options

- Rewrite the invariant to describe reality (reads and scratch loads error; carry-forward degrades) and fix the misleading empty-bag fall-back; cost: a backend switch stays a hard boundary for old rows.
- Make the erroring paths degrade to inline; cost: silently substitutes an empty value on the hot execution path, masking real loss.
- Split the rule by need — a current-run read errors, a historical copy degrades — and make the degrade honest; cost: two behaviours to state and test.

The ruling decides what a backend mismatch means for the reader.

## Ruling

> Recommended ruling (/verify-issues): Make the mismatch an error everywhere — including carry-forward, which today "degrades" to an empty bag — and rewrite the invariant to say so: switching blob backends with spilled rows outstanding is refused at read, loudly, until the rows are migrated or the old backend is restored.
>
> Rationale: an executor or a downstream node handed an empty attribute where a value should be is worse than a clear failure; three of four paths already chose the error, and the one degrading path was never delivering continuity. Flip case: if a backend migration tool lands that rewrites handles, the fall-back becomes moot and the invariant can promise continuity through migration instead.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
