---
issue: tag-move-route-skips-reserved-prefix-check
kind: audit
category: conflicting
artifacts:
  - concept:tag
status: retired
opened: 2026-08-16T09:05:05Z
---

# The tag-move route lets a scoped caller repoint a compose-reserved tag

Tags are movable names for template hashes. One prefix is reserved for the compose machinery. The tag concept says the server enforces the reservation on every path that attaches a tag to a template, dedicated tag-create and template registration alike. Tag-move repoints an existing tag at another hash. It gates only on the tag-set grant scoped to that tag, and it never runs the origin check. That path mints no new reserved tag, but a caller who holds the scoped grant can repoint an existing one at any template, and repointing is the whole content of what a tag is worth. The concept's other invariant treats the scoped move grant as sufficient governance for a move. The ruling decides which of its own two invariants governs.

## Options

- Add the reserved-prefix origin check to tag-move too; cost: a non-compose caller loses a scoped move grant an operator gave it deliberately on a compose tag.
- Scope the origin guard to attachment, meaning create and registration, and let the scoped move grant govern repointing; cost: leaves the exposure, and the concept must argue it acceptable.

The ruling decides whether "attach" includes "move".

## Ruling

Retired: the reservation goes. The `compose:` marker and its server-side fence guard against one thing: an operator hand-naming a tag into compose's namespace. That is not worth a header, a grant, and five checks. Compose keeps the manifest's project name as its prefix and finds its objects by it. The server reserves nothing. A tag or key another client names under a project's prefix is managed by compose like any other. The sprint planned in this session removes the fence and the `compose:` marker.
