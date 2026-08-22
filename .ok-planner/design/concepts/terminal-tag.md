---
concept: terminal-tag
---

# Terminal tag

## What it is

A terminal tag is one string in the tag set an executor attaches to a settling verdict — a success terminal, an error terminal, or a park. The set carries no duplicates: rimsky deduplicates the tags as it decodes the verdict. A tag labels that one emission and nothing beyond it. It rides the audit row, and for a verdict that ends the run it rides the subscriber's predicate evaluation; it never merges into node-attribute state.

## Purpose

A terminal tag gives an executor a topology-visible discriminator on its verdict that keeps no ledger. The tag's lifetime is the emission itself, which separates the discriminator role from the state-mutation role the same verdict carries beside it as an attributes delta (see `concept:attribute`). A subscriber that wants to fire on what kind of terminal this was predicates on tags; a subscriber that wants to fire on what the terminal changed predicates on the attributes delta. The two slots are independent, and one subscription predicate composes them freely. Past the declared-tag check rimsky reads a tag at two sites only — the cascade walk's predicate evaluation on a run-terminating verdict, and signal persistence — so a tag never merges into the per-run attribute ledger and never carries forward to the next dispatch. A park verdict carries tags too, but only for audit forensics: no subscriber fires on a park's tags, so the discriminator role reaches a subscriber only through the run-terminating settlement that eventually follows.

## Boundaries

A terminal tag owns the set semantics of its decode, the check of every tag against the emitting executor's declared-tag set, and the emission-scoped lifetime of the tag set on every verdict payload that carries one. The tag-name vocabulary is out: the executor's observability declaration is the registry (see also `concept:observability`, `concept:executor`). The subscription mechanism is out (see also `concept:node-subscription`), the cascade-fire mechanism is out (see also `concept:cascade`), and the persistent attribute mutation a settling verdict may also carry is out (see also `concept:attribute`). A terminal tag is distinct from `concept:tag`, a movable alias for a template hash; the two nouns share the word and nothing else — not meaning, not scope, not carrier.

See also `concept:signal`.
