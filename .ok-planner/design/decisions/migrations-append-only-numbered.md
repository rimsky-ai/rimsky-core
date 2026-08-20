---
decision: migrations-append-only-numbered
---

# Migration discipline

## Choice

Schema migrations are numerically ordered and maintained per backend (see `concept:persistence-database`). The sequence is append-only, with one bounded pre-v1 allowance: while every database is disposable, the project may collapse the applied sequence into a fresh baseline and reassign an ordinal a deleted migration once held. A migration taking that allowance states in its own text that operators drop and recreate their databases, because the allowance holds only for a database recreated against the current baseline. From v1 the sequence is append-only without exception: no file is edited, no file is removed, and no ordinal is reused.

The runner enforces the ordering and the immutability. It refuses to apply a file that sorts before one already applied. It also records a digest of each applied file's contents, so a file whose text changed after it was applied fails the next boot by name.

## Rationale

The migration runner's contract is a totally ordered, immutable sequence: numbering gives the order, append-only keeps every database's applied prefix valid forever. Rebasing that sequence breaks the contract on any database that already ran the old files, which is why the pre-v1 allowance is tied to dropping the databases rather than to the release alone — a disposable database has no applied prefix worth preserving. A pre-v1 schema rethink is expressed as a new drop-and-recreate migration wherever one will do (see `decision:migrations-no-compat-shims`); the allowance covers only the cases a new migration cannot express, such as collapsing a superseded sequence into its baseline.

The digest is what makes the v1 promise mechanical rather than remembered. Keying idempotency on the filename alone lets a rewritten file pass unnoticed, so a database that ran the old text and one that ran the new text diverge silently; comparing contents turns that into a loud refusal at the boot that would otherwise skip the file.

## Alternatives

- An external migration framework — rejected: a dependency for a job a small ordered per-backend runner already covers.
- Append-only with no pre-v1 allowance — rejected: it would force a compatibility shim or a dead ordinal for every pre-v1 schema rethink, buying nothing while the only databases in existence are disposable.
- An unbounded rebasing allowance carried past v1 — rejected: once a database holds data worth keeping, an edited migration is an undetectable divergence between two deployments claiming the same schema.
- Filename-only idempotency with no digest — rejected: it cannot tell a file that was never applied from one that was applied and then rewritten.
