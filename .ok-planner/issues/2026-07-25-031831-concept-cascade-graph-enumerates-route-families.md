---
issue: concept-cascade-graph-enumerates-route-families
kind: audit
category: other
artifacts:
  - concept:cascade-graph
status: verified
opened: 2026-07-25T03:18:31Z
---

# A definition document has turned into a route catalog

Rimsky's concept documents define what a component *is*; a house rule forbids them enumerating their own current instances — route paths, CLI verbs, wire literals — because that inventory belongs to code, and every addition would otherwise demand a doc edit. The concept for the read-only inspection surface (the HTTP endpoints operators use to see what's happening — event history, node and run status, dispatches, templates, lock ownership, peer status, health) violates this squarely: its "What it is" section lists nearly every route family by name, and the corpus's top-level index mirrors a shortened copy of the same list.

The wrinkle that makes this more than a mechanical trim: the concept is *named* for one of those routes. The "cascade graph" is the endpoint that joins a node with its downstream dependents — the nodes that re-run when it changes — into one graph-shaped result, and that join is arguably the concept's defining capability rather than one more list entry. So the ruling is really about where the line falls between the concept's identity (worth describing) and its inventory (worth deferring). The corpus already has a precedent posture for exactly this: two other concepts recently moved to "membership of the set is owned by the code, not enumerated here."

## Options

- **Full compression**: one general sentence for the whole surface; the route list lives in code alone. Cleanest fit; flattens the namesake join into the generic description.
- **Split**: keep the cascade-graph join description, compress the flat inventory of unrelated route families to one sentence, regenerate the index line to match.
- **Deliberate exception**: keep the list — a concept about a routes surface arguably is its routes — at the cost of an inventory the doc must chase forever.

The ruling decides: full compression or the split, and the index line follows.

## Ruling

> Recommended ruling (/recommend-rulings): Split: keep the per-
> instance cascade-graph join description (the concept's namesake
> capability), compress the flat route-family inventory to one general
> sentence deferring the route list to code, and regenerate the
> concepts.md TOC line to match.
>
> Rationale: The join is what the concept is; the inventory is what
> the code has — the same defer-membership posture signal and
> transition-reason already established.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
