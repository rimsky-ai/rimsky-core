---
experiment: claim-producer-conformance
commit: d977250c
---

# Proving a custom claim producer before shipping it

## What it ran against

A custom claim producer written for this experiment against the published
claim-producer gRPC protocol, started three ways on loopback: honest, one that
rejects a retried terminal verb, and one that blocks a reader while a writer
holds the byte-equal scope while still advertising staged-async write
semantics. `rimsky conformance claim-producer` — the shipped CLI verb — is
pointed at each endpoint in turn. No container and no rimsky stack are
involved: the author needs only their producer and the CLI.

## What was observed

Against the honest producer the suite ran 16 checks and printed one `ok` row
per check, including `Commit`, `Abandon`, `Release`, the three retry rows
(`TerminalIdempotency`, `AbandonTerminalIdempotency`,
`ReleaseTerminalIdempotency`) and `Serialization9b`, and exited 0.

Against the producer that rejects a retried terminal verb, the three retry
rows failed and each failure named the producer's rejection and said terminal
verbs must be idempotent under retry, the corresponding first-call rows
(`Commit`, `Abandon`, `Release`) still reported ok, the run printed
`3/16 checks failed`, and the command exited 1. The report is per check, not
a single verdict.

Against the producer that serialises a reader behind a writer, only
`Serialization9b` failed — the message names the reader-lease pattern as
forbidden for staged-async and names what honest support would require —
every other row including the terminal verbs reported ok, the run printed
`1/16 checks failed`, and the command exited 1.
