---
issue: tag-move-route-skips-reserved-prefix-check
kind: audit
category: conflicting
artifacts:
  - concept:tag
status: verified
opened: 2026-08-16T09:05:05Z
---

# The tag-move route lets a scoped caller repoint a compose-reserved tag

Tags are movable names for template hashes; a prefix is reserved for the compose machinery, and the tag concept says the server enforces the reservation on every path that attaches a tag to a template — dedicated tag-create and template registration alike. Tag-move (repointing an existing tag at another hash) gates only on the tag-set grant scoped to that tag and never runs the origin check. No new reserved tag can be minted that way, but an existing one can be repointed at any template by a caller who holds the scoped grant — and repointing is the whole content of what a tag is worth. The concept's other invariant already treats the scoped move grant as sufficient governance for a move. The ruling decides which of its own two invariants governs.

## Options

- Add the reserved-prefix origin check to tag-move too; cost: an operator who deliberately granted a scoped move on a compose tag to a non-compose caller loses that.
- Scope the origin guard to attachment (create, registration) and let the scoped move grant govern repointing; cost: leaves the exposure and must argue it acceptable.

The ruling decides whether "attach" includes "move".

## Ruling

> Recommended ruling (/verify-issues): Guard the move too — the reserved prefix is enforced on every path that changes what a compose tag points at, move included — and say so in the concept.
>
> Rationale: the reservation exists so nothing but compose decides what a compose tag resolves to; a move is exactly that decision, and a scoped grant is a permission to move a tag, not to impersonate compose. Flip case: if an operator workflow needs to hand a compose tag to another tool deliberately, that is a grant the compose-origin capability can carry — the exception rides the capability, not a hole in the check.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
