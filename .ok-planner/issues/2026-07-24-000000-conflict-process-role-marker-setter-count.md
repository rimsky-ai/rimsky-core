---
issue: conflict-process-role-marker-setter-count
kind: audit
category: conflicting
artifacts:
  - decision:single-process-mode
  - decision:process-role-unified-message-covers-rimsky-run
status: verified
opened: 2026-07-24T00:00:00Z
---

# Two decision docs count the same thing differently — one says three, one says four, the code says four

Rimsky has an environment-variable marker meaning "this process runs everything as one all-in-one deployment," and it gates a safety check: an in-memory storage backend is only legal when everything shares one process. Two decision documents describe who sets that marker — and disagree. One enumerates four code paths; the other enumerates three and stakes its whole point on the count being exhaustive ("names all three paths"). Both are marked current; both can't be right.

The code settles the count: four places set the marker — three genuine deployment paths (the all-in-one startup, and two CLI commands that run everything in-process) plus a fourth that isn't a deployment at all: an internal conformance-test runner sets it purely so its own in-memory-backend test passes the gate. The "three, exhaustively" document predates that fourth setter and was never revisited. Two ripples follow: the operator-facing error message shown when the gate trips also names only the three real paths (arguably correct for its audience, but exactly the kind of omission the exhaustiveness-minded document exists to prevent), and a third document — a user-expectation story — claims the marker is set "only" in genuine all-in-one mode, which the test-only setter also contradicts.

## Options

- **Merge the two decisions into one** stating four setters, and record the three-path error message as a deliberate audience choice.
- **Keep both, correct the stale one** — harder than a count bump, since its entire argument is "the message must name every setter."
- **Change the error message to name all four** — maximal consistency, at the cost of surfacing an internal test path to deployment operators.

The ruling decides: merge or keep both; whether the message stays at three by documented design or grows to four; and whether the story's "only" wording is corrected in the same pass.

## Ruling

> Recommended ruling (/recommend-rulings): Fold decision:process-role-
> unified-message-covers-rimsky-run into decision:single-process-mode.
> The surviving decision states four setters (matching code) and
> records the error text's three-path enumeration as deliberate — the
> conformance runner is not an operator path. Align story:single-
> process-all-in-one's 'set only in this mode' clause in the same
> delta.
>
> Rationale: One decision owning the marker removes the channel the
> drift came through; keeping the operator-facing message at three
> paths while the decision documents the omission serves both
> audiences honestly.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
