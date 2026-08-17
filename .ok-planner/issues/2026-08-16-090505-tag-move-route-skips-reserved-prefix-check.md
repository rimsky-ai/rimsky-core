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

Tags are movable names for template hashes. One prefix is reserved for the compose machinery. The tag concept says the server enforces the reservation on every path that attaches a tag to a template, dedicated tag-create and template registration alike. Tag-move repoints an existing tag at another hash. It gates only on the tag-set grant scoped to that tag, and it never runs the origin check. That path mints no new reserved tag, but a caller who holds the scoped grant can repoint an existing one at any template, and repointing is the whole content of what a tag is worth. The concept's other invariant treats the scoped move grant as sufficient governance for a move. The ruling decides which of its own two invariants governs.

## Options

- Add the reserved-prefix origin check to tag-move too; cost: a non-compose caller loses a scoped move grant an operator gave it deliberately on a compose tag.
- Scope the origin guard to attachment, meaning create and registration, and let the scoped move grant govern repointing; cost: leaves the exposure, and the concept must argue it acceptable.

The ruling decides whether "attach" includes "move".

## Ruling

> Recommended ruling (/verify-issues): Guard the move too. The server enforces the reserved prefix on every path that changes what a compose tag points at, move included, and the concept says so.
>
> Rationale: the reservation exists so that only compose decides what a compose tag resolves to. A move is that decision, and a scoped grant permits a caller to move a tag, not to act as compose. Flip case: an operator workflow may need to hand a compose tag to another tool deliberately. The compose-origin capability can carry that grant, so the exception rides the capability rather than a hole in the check.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
