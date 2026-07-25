---
issue: stale-tension-evidence
kind: human
category: docs-drift
status: verified
opened: 2026-07-22T02:38:08Z
---

# Three migrated design questions carry evidence the code has outgrown — is any cleanup actually needed?

Rimsky keeps a queue of open design questions. When an older format for that queue was migrated to the current one, three questions came across with their original evidence frozen in — and this issue claims all three descriptions have since gone stale against the code. Checking each: one (`timeout-policy-asymmetry`) describes machinery that has been entirely removed — the question dissolved; it also never reached the live queue, sitting only in an archive folder, still marked "open" in its own metadata. The other two (`stub-mode-signature-no-proto-surface`, `callback-hostname-split`) are live: their specific factual claims have partly rotted, but each one's core complaint remains true — and both received fresh verification passes this cycle, which re-checked their evidence anyway.

The real question left is pure housekeeping: does anything need doing? The project has an explicit doctrine that archived records are expected to drift and are *not* to be reconciled with current code — which covers the dissolved one exactly as it sits. And the standing verification process re-checks every live issue's evidence whenever it's processed — which just happened to the other two. One genuine finding did surface along the way: the design corpus's own summary of the stub-mode situation carries the same stale claims as the old question text, an independent drift instance now noted in that issue's file.

## Options

- **Do nothing** — archive doctrine covers the dissolved question; the live pair's evidence was already refreshed by their own passes. This issue's only output is confirming the staleness reading was accurate.
- **Reach into the archive** to mark the dissolved question resolved so its "open" metadata stops misleading future readers — more thorough, but breaks the do-not-touch-the-archive rule for a file no live process ever reads.
- **Record an explicit refresh note** against the two live issues — redundant with the verification that already happened, unless the owner wants belt-and-braces.

The ruling decides whether "nothing" is the answer, or the archive exception is worth making.

## Ruling

> Recommended ruling (/recommend-rulings): retire: everything the
> issue asks for is already covered — timeout-policy-asymmetry stays
> untouched in the archive per the don't-reconcile-history doctrine,
> and the other two issues' evidence was re-verified by their own
> /verify-issues passes this cycle.
>
> Rationale: The standing verification process is the refresh
> mechanism; recording that fact is this issue's only output.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
