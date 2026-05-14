# Atomic-staging pattern for custom ClaimProducers

## Why this pattern exists

A common cluster of consumer requirements does not fit the bundled
filesystem store's `pop_and_move` queue shape:

- The work product is the result of an *atomic backend transition* —
  a directory rename, a Postgres schema swap, a table-branch
  fast-forward, an S3 prefix flip. Operators want the storage backend
  to expose the new state to readers all at once or not at all.
- Pre-commit work products must be *visible* to verification nodes
  that run before the transition fires. Those verifiers want to see
  the staging contents (so they can run shape checks, domain checks,
  count comparisons) without exposing a partial state to downstream
  consumers.
- Multiple stagers may race; only one should win, and the others must
  observe the conflict without corrupting state.

The atomic-staging pattern packages these requirements into a
ClaimProducer implementation that participates in rimsky's held-claim
subgraph: a single acquirer Opens a staging area, downstream
verifiers Inherit the same claim, and the supervisor's auto-terminal
mechanism fires `Commit` (atomic transition) or `Abandon` (drop
staging) once the holding subgraph completes.

This pattern is worth a worked example because the backend semantics
— what "atomic" really means, what guarantees survive a crash, how
stagers conflict — vary substantially between storage backends even
though the protocol surface (4 verbs + Capabilities) is identical.

## The pattern

A custom ClaimProducer implements the four verbs plus `Capabilities()`
declaring `write_semantics_allowed: [staged_async]`.

### `Open(scope, claim_id, intent)`

The acquirer's Open creates a staging area for the (scope, claim_id)
pair. Per-backend examples:

- **POSIX filesystem**: `mkdir -p staging/<scope>/<claim_id>/` plus a
  side-table entry recording `(claim_id, staging_path,
  canonical_path)` so the sweep loop can find leaked staging.
- **Postgres schema swap**: `CREATE SCHEMA staging_<claim_id>` (or
  `staging_<scope>_<claim_id>`); record `(claim_id, schema_name)`.
- **S3 prefix copy**: track the intended target prefix; the copy
  itself happens during executor writes (the staging area is just a
  conceptual placeholder until Commit).
- **Iceberg branch**: `CREATE BRANCH staging_<claim_id> FROM main`;
  record the branch name.

The address returned to rimsky is the staging area path / schema
name / branch name. Verify nodes read this via
`{{claim.<alias>.address}}` and run their checks against it.

### `Commit(claim_id)`

Fire the atomic backend transition. The pattern's name comes from
*this* step:

- **POSIX**: two-rename atomic swap. `mv canonical/<scope>
  canonical/<scope>._old`; `mv staging/<scope>/<claim_id>
  canonical/<scope>`; `rm -rf canonical/<scope>._old`. The window
  between renames is short but non-zero; readers that hit it see
  either the old or the new state.
- **Postgres**: single transaction. `ALTER SCHEMA canonical_<scope>
  RENAME TO canonical_<scope>_old`; `ALTER SCHEMA staging_<claim_id>
  RENAME TO canonical_<scope>`; `DROP SCHEMA canonical_<scope>_old
  CASCADE`. Atomic with respect to readers via MVCC.
- **S3**: copy from staging prefix to canonical prefix; delete from
  staging. *Not atomic in the strong sense* — readers can observe a
  partial new state. See "Atomicity caveats" below.
- **Iceberg**: fast-forward main to the staging branch's HEAD.
  Atomic at the metadata level.

### `Abandon(claim_id)`

Drop the staging area without firing the transition. Cleans up
filesystem dirs, schemas, branches, prefixes, etc.

### `Release(claim_id)`

For `intent: r` readers, no-op. For `intent: rw` claims that never
committed (e.g. the run was killed before terminal), equivalent to
`Abandon`.

### `Capabilities()`

Declare `protocols: [claim_producer]` (plus `lifecycle_subscriber` if
applicable), `write_semantics_allowed: [staged_async]`, and the
scope-conflict matrix the backend supports. Standard implementations:
byte-equal scope only; richer producers may declare prefix-based or
pattern-based conflict.

## Atomicity caveats by storage backend

The four-verb protocol is uniform; what "Commit fires atomically"
means is not.

- **POSIX rename**: atomic on the same filesystem. Cross-filesystem
  renames fall back to copy-then-delete (non-atomic). The window
  between the two renames is brief but not zero.
- **Postgres single-tx**: atomic with respect to readers via MVCC.
  Holding multiple `ALTER SCHEMA` in one transaction is supported.
- **Iceberg branch fast-forward**: atomic at the metadata level (one
  HEAD pointer write). Underlying file uploads are eventually
  consistent against the consumer; producers should fence on read.
- **S3 copy+delete**: non-atomic. Readers can observe a half-flipped
  state during the operation. For S3-style backends, prefer
  multi-part upload + manifest-based discovery, or use Iceberg or a
  similar table-format layer.
- **BigQuery**: backend-dependent. `INSERT OVERWRITE` is atomic;
  `CREATE OR REPLACE TABLE` is atomic; copying via streaming inserts
  is not.
- **Streaming backends** (Kafka, Kinesis): "atomic commit" does
  not apply at the message level; producers must commit at the
  consumer-offset level, which is a different shape entirely. The
  atomic-staging pattern is not a good fit.

Producers SHOULD document their atomicity guarantees in their
README and in the `Capabilities` response.

## Held-subgraph integration

The atomic-staging pattern composes with rimsky's held-claim
discipline:

- **Acquirer** Opens the staging area. Returns `staged_async`
  semantics. Does not Commit.
- **Verifier nodes** declare `inherits: [{claim: <alias>}]`. They
  subscribe to the acquirer's state (via the post-2026-05-14
  `subscribes:` model) and read the staging address from the
  inherited claim. They run shape / domain / count checks against
  staging.
- **Holding subgraph** = acquirer + every inheritor. All members run
  to terminal (fresh or failed).
- **Auto-terminal** fires when every claim-holder is non-active. If
  every member succeeded → `Commit`. If any failed → `Abandon`.

Verify nodes are free to fail (e.g. `verify-staged-shape` flunks the
shape check). Auto-terminal routes the aggregate `Abandon` and the
producer drops staging — the storage backend never observes the
staged state.

## Concurrent stagers and orphan handling

Rimsky serializes byte-equal scope at the claim-handle ledger: two
acquirers with byte-equal scope bytes cannot both hold the handle.
The losing acquirer's Open returns `Unavailable` and the supervisor
routes through `on_acquire_unavailable` (typical: `resolve: pass` so
the loser stays fresh without doing work; the winner runs).

The producer still needs a sweep loop for *leaked* staging — runs
that crashed between Open and any terminal. The sweep loop:

- Periodically queries the claim-handle ledger over Postgres (or
  whatever rimsky-side authority the producer can read).
- For each staging entry in the producer's side-table whose
  `claim_id` isn't in the live handle set AND was created more than
  `staging_ttl` ago, calls Abandon (or the backend-equivalent drop).
- Cadence: 5 minutes is typical; TTL: 24 hours is conservative.

The TTL is operator-tunable. Aggressive sweeps (TTL = 5 minutes)
reclaim faster but risk dropping legitimately-long-running
acquisitions; conservative sweeps leak disk / schema space briefly
in exchange for safety.

## Worked example: filesystem

The reference filesystem implementation lives under
`examples/atomic-staging-fs-producer/` in the rimsky repository.
Structure:

- `cmd/main.go` — binary entrypoint; reads env, starts the gRPC
  server, spawns the sweep loop.
- `server/server.go` — gRPC `ClaimProducerServer` implementation;
  one method per verb plus `Capabilities`.
- `store/store.go` — the four-verb logic against the filesystem +
  SQLite producer state.
- `sweep/sweep.go` — the leaked-staging reaper.
- `template.yaml` — a worked-example template using the producer
  (one acquirer + two verifiers).

See the example's `README.md` for build / run instructions.

## See also

- `docs/concepts/claim-producer.md` — the protocol surface (Open /
  Commit / Abandon / Release / Capabilities).
- `docs/concepts/claim-producer-fs-store.md` — the bundled
  `pop_and_move` shape (related but queue-shaped, not staging-shaped).
- `docs/concepts/held-subgraph.md` — held-subgraph discipline +
  auto-terminal mechanism.
