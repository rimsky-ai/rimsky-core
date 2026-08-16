---
issue: per-module-prose-sweep-has-no-subject
kind: audit
category: conflicting
artifacts:
  - decision:untagged-prose-by-module
status: verified
opened: 2026-08-16T10:00:04Z
---

# The per-module prose-sweep decision governs a backlog that no longer exists

A decision decomposes the comment-hygiene remediation backlog into one sweep per top-level module root. The backlog is zero: the lint runs over the whole repo and the suite fails on any violation, no ratchet file exists, and the two sweeps that cleared the tree each landed as one change across every root. The decision has no current subject and the history it describes did not follow it. The ruling decides whether a rule for hypothetical future sweeps stays.

## Options

- Retire it as complete; cost: loses a reusable convention for future judgment-heavy sweeps.
- Restate it as a standing rule for future sweeps of this shape; cost: changes its claim from "what we did" to "what we will do", which the owner must want.
- Keep it and note history did not follow it; cost: a rule with no force and a non-compliance nobody acts on.

The ruling decides whether a process rule with no subject stays on the books.

## Ruling

> Recommended ruling (/verify-issues): Retire it — the corpus is current-state only, and a decomposition rule for a drained backlog describes neither a current commitment nor a mechanism the tree has.
>
> Rationale: the lint gates the whole tree at zero, which is a stricter rule than any per-root sweep, and a future large sweep can decompose itself under its sprint's own plan without a standing decision. Flip case: if the owner expects another judgment-heavy backlog soon (a new lint check with thousands of hits), restating it as a standing rule is worth the sentence.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
