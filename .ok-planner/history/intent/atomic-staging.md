# Intent Dossier: atomic-staging

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Atomic staging is a **producer-side pattern**: Open reserves a private staging area (returned with `staged_async` semantics), the executor writes into staging, Commit atomically swaps staging into the canonical view, Abandon discards staging leaving canonical untouched. The atomicity lives in the producer, not in rimsky.
- It is the all-or-nothing default the held-subgraph model composes with: Commit on all-success, Abandon on any-failure; on a member failure the staged data stays un-swapped and holders go to failed with terminal/error/abandoned.
- Two shipped carriers today, per the latest record: the **postgres bundled store** (real schema-swap staging for schema-shaped selectors) and the **examples/atomic-staging-fs-producer** copyable example. The **bundled filesystem store deliberately does in-place writes with no atomic staging** and advertises sync-only write-semantics — the corpus-bootstrap story claiming a filesystem-store atomic swap was adjudicated overstated, fix-doc (2026-07-13, finding 1763).
- Swap failure is a routed classed error (`pg/swap_failed`, google.rpc.ErrorInfo domain `rimsky.store-postgres`) decoded into the holder's `error_types` routing — never a tx-fatal error that would wedge the auto-terminal transaction.

## Required behaviors (open promises)

- Pattern semantics: staging per claim; Commit = atomic swap (two-rename on one filesystem / schema swap in postgres — never copy-then-overwrite); Abandon = drop staging, canonical untouched; Release of an uncommitted rw claim ≡ Abandon (2026-05-14, subscription-cascade-and-quality-of-life, artifact).
- Held subgraphs commit-or-abandon atomically through the pattern: aggregate success swaps staged data into the canonical view; any failure drops staging (2026-06-02, acceptance-coverage-recovery, artifact).
- On a held-subgraph member failure, the holder/acquirer auto-transitions to Failed with terminal/error/abandoned while staged changes stay un-swapped — abandon is a holder-failure state, not a separate lock-abandon phase; tests expecting the acquirer to stay fresh were realigned to this (2026-06-29, 8a8539a4, transcript).
- Postgres store as a real staging substrate: Open reserves a staging schema, Commit performs an atomic schema swap, Abandon discards the staged schema; failed swap surfaces `pg/swap_failed` routable through error_types; `pg/claim_unavailable` on empty pick queue (2026-06-06 gap-closure + 2026-06-08 corpus-bootstrap, artifact).
- The postgres staging lifecycle engages only for schema-shaped selectors (`^[a-z_][a-z0-9_]*$`); opaque/path-shaped scope-bytes claims keep the verbatim selector-echo Open and no-op terminals; the canonical drop uses DROP SCHEMA RESTRICT — a populated or externally-depended-upon canonical is refused (that refusal IS `pg/swap_failed`), never silently clobbered (2026-06-06, divergences, artifact, accepted as intended design).
- `pg/swap_failed` surfaces at the gRPC terminal-verb boundary as a classed signal, not tx-fatal (2026-06-06, artifact).
- The copyable example lives at examples/atomic-staging-fs-producer/ (Apache-licensed module) with its four scenario tests (abandon-on-any-failure, commit-on-all-success, concurrent-staging, sub-stage-verifier-failure) as tests-as-documentation running without a rimsky harness (2026-06-06 restoration + 2026-05-24 repo-reorganization, artifact).
- Example-producer hygiene: periodic sweep dropping staging dirs older than a configured TTL (default 24h) whose claim_id is not live, and refusal to start when staging and canonical roots are on different filesystems (2026-05-14, artifact-only).
- Verifier-executor integration: the postgres store registers Executor alongside ClaimProducer (one binary, two roles); checks compile to aggregate-only SELECT-prefixed queries (pinned by test); all-pass emits Success, any failure emits `verifier_failed`, feeding the Commit/Abandon aggregation (2026-05-19, multi-instance-template-ergonomics + 2026-06-08 `row_count_ratio`, artifact).
- Substrate caveats stay documented per substrate: Postgres schema swap and Iceberg branch fast-forward atomic; POSIX rename atomic within a filesystem; S3 copy+delete windowed; Kafka incoherent for the pattern (2026-05-15, data-platform-extensions, artifact-only).

## Intentional absences

- **Atomic staging in the bundled filesystem store** — deliberate: in-place writes, sync-only write-semantics from day one; write-semantics permits the sync-only subset (2026-07-13, 3f71f90a, transcript; adjudicated fix-doc, finding 1763). Findings expecting a filesystem-store swap assert drifted expectations.
- **Rimsky-side atomicity machinery** — none by design; the pattern is producer-internal (2026-05-14).
- **Verifier queries reading row data** — SELECT-only aggregate checks by construction (2026-05-19).

## Corrections and restorations (drift-fight record)

- **Reference producer deleted wholesale** (2026-06-06, gap-closure): the stage-then-swap filesystem reference producer was built in e1487e1 then deleted in c1ce756; ruled promised-capability-missing and restored as a fresh examples/ module rather than a git-recovery.
- **Postgres no-op staging made real** (2026-06-06): the 2026-06-02 position that the postgres store's no-op Commit/Abandon was "by design, a separate unshipped feature" was superseded by building the real schema-swap.
- **Filesystem-store story overstated** (2026-07-13): corpus-bootstrap prose claimed an atomic swap the bundled store never did; adjudicated fix-doc, not fix-code.

## Superseded / historical

- 2026-06-02 rejection ("SQL-substrate stage-then-swap is a separate unshipped feature, not a coverage gap") → superseded by the 2026-06-06 postgres staging implementation.
- 2026-06-06/06-08 artifact claims that the bundled filesystem store gained staging / performs atomic rename swaps → superseded by the 2026-07-13 transcript ruling that the store is deliberately in-place, sync-only (transcript outranks artifact). The staging behavior lives in the example producer.
- Planned end-to-end atomic-staging scenario (staging→verify→commit + Abandon failure path) → replaced at execution with protocol-shape-only plus substrate-level tests; the supervisor's terminal-routing contract asserted by shape, not end-to-end (2026-05-19, divergences, artifact-only). Post-reorg, the two pg-verifier atomic-staging tests moved as t.Skip stubs pending an image bring-up harness (2026-05-24) — recorded coverage debt, not a behavioral retraction.
- Atomic-staging recorded as having **no in-repo @concept annotation site** during the 2026-05-25 backfill — the concept was doc-only at that date; adjudicators should not expect a load-bearing citation site from that era.
