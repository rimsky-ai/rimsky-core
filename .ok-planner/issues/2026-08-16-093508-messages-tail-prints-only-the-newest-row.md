---
issue: messages-tail-prints-only-the-newest-row
kind: audit
category: conflicting
artifacts:
  - concept:rimsky
  - concept:message
status: verified
opened: 2026-08-16T09:35:08Z
---

# The CLI's message tail prints only the newest row of each page

The messages listing route returns pages newest-first. The CLI's tail verb walks each page with a watermark meant for an ascending stream: it prints the first (newest) row, advances the watermark to that row's time, and then skips every remaining older row in the same page as already seen. Without follow the operator sees one row; with follow, each poll drops the backlog the same way. The route is correct and complete; the defect is entirely client-side. The ruling fixes the loop.

## Options

- Compare each page's rows against the previous poll's watermark, print every row that passes, and only then advance the watermark from the page's newest row; cost: none.
- The same, plus a decision fixing a route-ordering convention; cost: heavier than one affected verb warrants.

The ruling fixes the client loop.

## Ruling

> Generated ruling (/verify-issues): Rewrite the tail loop so it filters the whole page against the watermark taken before the poll and advances the watermark only after the page is printed — every row of a received page reaches the operator, in one-shot and follow modes alike. Forced by the plain defect (a descending page walked with an ascending watermark); no corpus change is needed to fix it. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
