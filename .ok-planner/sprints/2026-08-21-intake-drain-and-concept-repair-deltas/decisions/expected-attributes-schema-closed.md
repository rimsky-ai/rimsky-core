---
decision: expected-attributes-schema-closed
---

# An executor's expected-attributes schema is a closed contract

## Choice

The expected-attributes schema an executor advertises declares every node-attribute key the executor reads and admits no other. Template registration rejects a node whose attribute block carries a key the schema does not declare. A fitness check keeps each bundled executor's advertised schema equal to the set of keys its code reads (see `concept:observability`, `concept:executor`).

## Rationale

The schema exists to catch an authoring mistake before dispatch. Every third-party executor copies a bundled one as its reference, so a bundled schema that declares one key and reads five is the wrong model to copy, and a schema that admits undeclared keys catches no misspelling at all.

## Alternatives

- The schema as a partial hint that admits additional keys — rejected: it catches no misspelled key, which is the check's purpose.
- Open-ended attribute bags passed through to a script — not taken: if an executor is meant to accept one, the concept must stop promising that rimsky catches misspelled keys, and no bundled executor is meant to.
