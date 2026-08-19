---
issue: sensor-state-stores-use-rejected-sql-adapter
kind: audit
category: conflicting
artifacts:
  - decision:postgres-pgx-v5
status: promoted
opened: 2026-08-16T09:30:04Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# Four sensor state stores reach Postgres through the adapter the driver decision rejects

Four sensor state stores reach Postgres through the standard-library adapter, which the Postgres-driver decision rejects. The decision says access uses the native driver interface. Its rationale names what the persistence layer needs: structured errors, native type mapping, pooling. Fifteen Postgres touchpoints use the native interface. The four sensors' small state stores open a generic handle through the adapter. Nothing breaks. The dependency lint governs where the driver may be imported, not which of its surfaces the code uses, so nothing catches this. The decision's Choice reads project-wide. Its rationale reads persistence-layer. The ruling decides which reading governs.

## Options

- Convert the four state stores to the native interface and add a lint forbidding the adapter import anywhere; cost: rewriting four small caches that gain little from pooling or structured errors.
- Rescope the decision to the persistence layer and claim producers, which is what its rationale argues, and permit a bundled service's own state store to use the adapter; cost: two idioms for reaching Postgres, and a future contributor could copy the wrong one into the wrong place.

The ruling decides how far the native-only rule reaches.

## Ruling

> Recommended ruling (/verify-issues): Convert the four sensor state stores to the native interface and add the lint, so the repo keeps one idiom.
>
> Rationale: the project's uniformity rule is one way per job. Two Postgres idioms invite the wrong one into the persistence layer later, and the conversion touches four small files. Flip case: if the sensors are meant to stay backend-agnostic, with a state store any SQL database can back, the adapter is the right abstraction there and the decision should be rescoped to say so.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
