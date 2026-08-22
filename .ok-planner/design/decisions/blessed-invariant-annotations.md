---
decision: blessed-invariant-annotations
---

# Safety properties are named in decisions and stories, not tagged by number

## Choice

A load-bearing safety property is recorded by descriptive name in the decision or story that owns it, proven by a test, and cited from the enforcing code through the ordinary `@decision:` or `@story:` annotation. There is no dedicated invariant annotation and no numbered-invariant catalog. A concept defines a noun and records no safety property. Tests covering a property name it descriptively in the same way.

## Rationale

A numbered catalog paired with a dedicated tag imposes a separate naming scheme on top of the design corpus: a property carries a number, a name buried in an artifact body, and a lint-enforced tag — and the number is the weakest of the three, opaque at the call site, fragile across renumberings, and duplicated by the slug it accompanies. Recording each property in the artifact whose commitment it is collapses the three surfaces to one: artifact slugs are already the project's stable identity layer, already linked from code by annotation, and already audited. Coverage is read from the tests that prove each property, not from a tag-coverage lint over numbered slots.

## Alternatives

- A dedicated invariant tag fed by a numbered catalog — rejected: the redundancy buys nothing beyond the artifact-slug surface and adds a separate convention to maintain.
- Keeping the properties in the concept catalog under a per-concept invariants section — rejected: a concept defines a noun, and a guarantee stated there is a commitment filed under the wrong kind.
