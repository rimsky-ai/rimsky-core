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

Each executor advertises a JSON schema for the node attributes it expects; the observability concept says rimsky validates a template's attributes against it at registration and again at dispatch, which is what lets a misspelled key be caught. The verifier-http executor advertises one key and reads five; the shape-checks verifier advertises one and reads two more; the http-node executor advertises an unconstrained object and reads nine. So a misspelled key is silently dropped at both gates and the executor runs with a default — the declaration cannot do the job the concept gives it. The ruling decides what the advertised schema promises.

## Options

- Add an invariant that every dispatch-read key appears in the advertised schema, and a fitness test diffing read keys against declared ones; cost: widening three schemas and keeping them honest.
- Decide the schema is a closed contract (no additional properties, every read key declared) so an undeclared key is a registration error; cost: a behaviour change for templates that carry extra keys.
- Narrow the concept to "an optional partial hint"; cost: concedes the misspelling-catch purpose.

The ruling decides whether the schema is a contract or a hint.

## Ruling

> Recommended ruling (/verify-issues): Make it a contract — every key an executor reads is declared, unknown keys are rejected at registration, and a fitness test keeps the three bundled schemas equal to what their code reads.
>
> Rationale: the concept's stated purpose is catching authoring mistakes early, and the bundled executors are the reference every third-party executor copies; a hint that omits four-fifths of the keys teaches the wrong lesson. Flip case: if executors are meant to accept open-ended attribute bags by design (pass-through to a script), the third option is honest and the concept should stop promising misspelling detection.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
