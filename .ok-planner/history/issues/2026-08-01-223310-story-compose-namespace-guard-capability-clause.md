---
issue: story-compose-namespace-guard-capability-clause
kind: sprint
category: stories-prescriptive
artifacts:
  - story:compose-namespace-guard
  - concept:tag
  - concept:rimsky
  - concept:control-api
status: answered
opened: 2026-08-01T22:33:10Z
---

# Does compose-namespace-guard's dropped capability clause name a mechanism no artifact documents?

Question: `story:compose-namespace-guard` was reduced to its canonical sentence, dropping "(holding the appropriate capability)". This issue was filed claiming that clause named a capability-based enforcement mechanism no decision documents, so the reduction removed an uncarried commitment.

Answer: no — the mechanism is documented, in three places, under different wording.

- `concept:tag` invariant: the `compose:<project>:<...>` tag prefix is "reserved and **server-enforced** on every path that can attach a tag to a template — dedicated tag-create and template registration alike — rejecting a `compose:`-prefixed name unless the request originates from the privileged compose path."
- `concept:rimsky` invariant: "The compose-tag prefix reservation on tag and instance-key namespaces is server-enforced; the CLI's compose workflow identifies itself as the compose origin so the server permits it to create prefixed tags and instance keys." The same concept's Boundaries make origination of compose-prefixed tags and instance keys, "as the server's designated compose origin", an owned responsibility.
- `concept:control-api` invariant: "The reserved `compose:` prefix on tags and instance keys is server-enforced: requests originating outside the CLI's compose surface (`concept:rimsky`) are rejected when they target it."

"Holding the appropriate capability" and "originating from the privileged compose path" name the same privileged-origin check; the story's parenthetical was a per-story paraphrase of an enforcement rule the concept catalog already owns. The reduction preserved every commitment, the reduced story stands, and there is nothing here for a sprint to carry.
