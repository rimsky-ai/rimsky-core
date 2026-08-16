---
issue: child-execution-close-paths-incomplete
kind: audit
category: inconsistent
artifacts:
  - concept:child-execution
  - concept:run-scope
status: verified
opened: 2026-08-16T08:47:38Z
---

# The child-execution concept enumerates closure paths that a sibling concept already owns and gets them wrong

A delegated child execution runs in its own scope; the child-execution concept says settlement is the only run-side path that closes such scopes, with administrative instance termination the sole exception. There is a third production path: when a frame settles, the frame engine sweeps the settled frame's scope tree deepest-first, closes any straggler child scope still open, and fires each scope's terminal fan-out — without it, orphaned child scopes would never close and peers would never hear about them. The run-scope concept already names all three paths correctly, and the child-execution concept's own Boundaries section assigns scope lifecycle to run-scope. The ruling decides that the enumeration leaves child-execution.

## Options

- Add the third path to child-execution too; cost: duplicates what run-scope owns and recreates the drift.
- Drop the enumeration from child-execution and let run-scope hold sole authority, keeping child-execution's own claim (settlement carries the outcome atomically with closure); cost: none — it matches the ownership the corpus already states.

The ruling decides which concept lists closure paths.

## Ruling

> Generated ruling (/verify-issues): Remove the closure-path enumeration from the child-execution concept and defer to the run-scope concept, which already lists all three run-side closure paths correctly; keep child-execution's own invariant to what settlement uniquely does — carrying the outcome atomically with the close. Forced by the corpus's own ownership split (child-execution's Boundaries assign scope lifecycle to run-scope) and the self-containment rule. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
