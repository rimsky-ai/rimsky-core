---
issue: advertised-attribute-schemas-understate-accepted-keys
kind: audit
category: inconsistent
artifacts:
  - concept:observability
  - concept:executor
status: verified
opened: 2026-08-16T09:35:01Z
---

# Three bundled executors advertise attribute schemas that omit most of the keys they read

Each executor advertises a JSON schema for the node attributes it expects. The observability concept says rimsky validates a template's attributes against that schema at registration and again at dispatch, and that check catches a misspelled key. The verifier-http executor advertises one key and reads five. The shape-checks verifier advertises one key and reads two more. The http-node executor advertises an unconstrained object and reads nine. Both gates therefore drop a misspelled key silently, and the executor runs with a default. The declaration cannot serve the purpose the concept gives it. The ruling decides what the advertised schema promises.

## Options

- Add an invariant that every key an executor reads at dispatch appears in its advertised schema, and a fitness test that diffs read keys against declared keys; cost: widening three schemas and keeping them accurate.
- Make the schema a closed contract that allows no additional properties and declares every key the executor reads, so registration rejects an undeclared key; cost: a behaviour change for templates that carry extra keys.
- Narrow the concept to "an optional partial hint"; cost: gives up catching misspelled keys.

The ruling decides whether the schema is a contract or a hint.

## Ruling

> Recommended ruling (/verify-issues): Make the schema a contract. Every key an executor reads is declared. Registration rejects an unknown key. A fitness test keeps the three bundled schemas equal to what their code reads.
>
> Rationale: the concept exists to catch authoring mistakes early. Every third-party executor copies a bundled executor as its reference. A schema that omits four-fifths of the keys is the wrong model to copy. Flip case: if executors are meant to accept open-ended attribute bags by design, passing them through to a script, the third option is honest and the concept should stop promising that rimsky catches misspelled keys.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
