---
decision: blessed-invariant-annotations
status: as-is
---

# Retire the blessed-invariant tag and the numbered-invariant convention

## Choice

The project no longer uses the `@blessed-invariant` source-code annotation, and the numbered-invariant catalog that fed it is also retired. Load-bearing safety properties are documented by descriptive name in the relevant concept doc (`concept:<slug>`), and code sites that enforce them cite the concept via `@concept:` rather than a number. Tests covering those constraints reference them by descriptive name in the same way.

## Rationale

The numbered list and the tag pair imposed a separate naming scheme on top of the concept catalog: a constraint had a number, a name buried in the concept body (e.g. "claimant-guarded release"), and a tag the lint enforced. The number was the weakest of the three — opaque at the call site, fragile across renumberings, and duplicated by the concept slug it usually accompanied. Naming each constraint descriptively inside its owning concept doc collapses the three surfaces to one: concept slugs are already the project's stable identity layer, already linked from code via `@concept:` annotations, and already audited by the concept self-containment rule. Tests of a constraint cite the descriptive name in their assertion messages or test names; coverage is verified by reading those, not by a tag-coverage lint over numbered slots.

## Alternatives

Keeping the numbered catalog and the `@blessed-invariant` tag (the prior arrangement): rejected — the redundancy bought nothing beyond the concept-slug surface and added a separate convention to maintain.
