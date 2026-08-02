---
decision: blessed-invariant-annotations
---

# Safety properties are named in concept docs, not tagged by number

## Choice

Load-bearing safety properties are documented by descriptive name in the relevant concept doc, and code sites that enforce them cite the concept via `@concept:` — there is no dedicated invariant annotation and no numbered-invariant catalog. Tests covering those constraints reference them by descriptive name in the same way.

## Rationale

A numbered catalog paired with a dedicated tag imposes a separate naming scheme on top of the concept catalog: a constraint carries a number, a name buried in the concept body (e.g. "claimant-guarded release"), and a lint-enforced tag — and the number is the weakest of the three, opaque at the call site, fragile across renumberings, and duplicated by the concept slug it accompanies. Naming each constraint descriptively inside its owning concept doc collapses the three surfaces to one: concept slugs are already the project's stable identity layer, already linked from code via `@concept:` annotations, and already audited by the concept self-containment rule. Tests of a constraint cite the descriptive name in their assertion messages or test names; coverage is verified by reading those, not by a tag-coverage lint over numbered slots.

## Alternatives

- A dedicated invariant tag fed by a numbered catalog (the `@blessed-invariant` arrangement) — rejected: the redundancy buys nothing beyond the concept-slug surface and adds a separate convention to maintain.
