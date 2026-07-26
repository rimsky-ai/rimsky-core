---
issue: concepts-name-backend-products
kind: audit
category: compliance
artifacts:
  - concept:advisory-lock
  - concept:persistence-database
status: verified
opened: 2026-07-25T21:11:30Z
---

# Should the two persistence concepts name Postgres and SQLite, or stay product-neutral?

Two concepts name their backing products structurally. The persistence-database concept defines itself as "two impls: a Postgres adapter and an SQLite adapter," and the advisory-lock concept's invariants describe per-backend mechanics — native session locks on the server-backed store, file locks and single-writer transaction rules on the embedded one. Three decisions already exist whose entire job is carrying those product identities and their tradeoffs (`decision:persistence-dual-backend`, `decision:postgres-pgx-v5`, `decision:sqlite-modernc-pure-go`), and the corpus's own sibling — the blob-backend concept — deliberately stays abstract, naming roles ("where the bytes live") rather than products.

What makes this a real calibration rather than a rule application: the behavioral fork in these two concepts is load-bearing, not cosmetic. An advisory lock that is a database session lock behaves differently from one that is a file lock, and the concepts' invariants genuinely range over that fork. The question is whether the fork can be described in role language (client-server backend vs. embedded file backend) with the product names left to the decisions, or whether that abstraction would cost more clarity than it buys.

## Options

- Generalize both concepts to role language (a client-server backend, an embedded single-file backend), keeping every behavioral invariant but leaving product names to the three decisions — consistent with the blob-backend precedent; costs a little immediacy for readers who know the products.
- Keep the product names as a deliberate exception where behavior forks per backend — costs consistency and leaves the concepts hostage to a future backend swap.

## Ruling

> Recommended ruling (/verify-issues): generalize `concept:advisory-lock` and
> `concept:persistence-database` to role language — the client-server backend and the
> embedded file backend — preserving every per-backend invariant, with product identity
> left to the three existing decisions.
>
> Rationale: the corpus already settled this calibration once, in the blob-backend
> concept, and the three product decisions exist precisely to carry the names; the
> behavioral fork survives abstraction intact, so the exception buys nothing the
> role language cannot express.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
