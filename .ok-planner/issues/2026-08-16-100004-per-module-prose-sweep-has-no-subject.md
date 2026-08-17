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

A decision splits the comment-hygiene remediation backlog into one sweep per top-level module root. The backlog is zero. The lint runs over the whole repo, and the suite fails on any violation. No ratchet file exists. The two sweeps that cleared the tree each landed as one change across every root. The decision therefore has no current subject. The history it describes did not follow it. The ruling decides whether a rule for hypothetical future sweeps stays.

## Options

- Retire the decision as complete; cost: the corpus loses a reusable convention for future judgment-heavy sweeps.
- Restate it as a standing rule for future sweeps of this shape; cost: its claim changes from "what we did" to "what we will do", and the owner must want that change.
- Keep it and note that history did not follow it; cost: a rule with no force, and a non-compliance nobody acts on.

The ruling decides whether the corpus keeps a process rule with no subject.

## Ruling

> Recommended ruling (/verify-issues): Retire the decision. The corpus states current state only, and a rule for splitting an empty backlog describes no current commitment and no mechanism the tree has.
>
> Rationale: the lint gates the whole tree at zero, a stricter rule than any per-root sweep. A future large sweep can split itself under its sprint's own plan without a standing decision. Flip case: if the owner expects another judgment-heavy backlog soon, such as a new lint check with thousands of hits, restating it as a standing rule is worth the sentence.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
