# Intent Dossier: claim

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- A claim and a claim-handle are two layer-distinct nouns for one conceptual thing: claim is the protocol-layer noun returned by ClaimProducer.Open; claim-handle is the rimsky-persistence-layer noun. Content inertness gates the former, claimant-guarded release gates the latter (2026-05-11, log-convergence, artifact).
- The ClaimProducer protocol is five RPCs (Capabilities, Open, Commit, Abandon, Release); OpenResponse is a oneof of Acquired / Unavailable; claim_id is rimsky-generated (2026-05-04, service-protocol-contract, artifact).
- Claim content (address, payload, scope) is byte-opaque to rimsky (inertness invariant 20). Claim intent is a two-value vocabulary ("r"/"rw"); write-semantics is a four-value vocabulary carried as a producer envelope plus a per-claim realized value on the handle (2026-05-04, service-protocol-contract, artifact).
- Intent is a cascade-layer contract enforced by the coexistence predicate (ModeCoexists), not a producer-side concern: producers validate intent shape at Open and correctly ignore it afterward (2026-06-18, 8e7e4c10, transcript).
- A claim is "just a lock." A parked node's claim is not released; contention against a parked holder uses regular lock semantics with no preemption (2026-06-16, 055468fc, transcript). Parked and pending-claim are legitimate waiting states, not degenerate cases (2026-06-16, 4c42fe5b, transcript).
- A sub-claim is a claim — parity where it earns its keep: sub-claims inherit the parent's intent and realized_write_semantics; payload persists on all claim rows uniformly (2026-06-18, 9fb55f08 / 8e7e4c10, transcript).
- Claim acquisition happens at stale→running; substitution runs in exactly two rounds (upstream/attribute at pending→stale, claim at stale→running) on one uniform code path (2026-06-21, 10cf843b, transcript).
- Retry — for executor errors and claim-acquisition errors alike — is in-place on the same node_run with the claim held, through one unified error-policy path (2026-06-22, 10cf843b, transcript).
- Fan-out is a claim request to a producer, completely orthogonal to messages (2026-06-18, 9fb55f08, transcript).

## Required behaviors (open promises)

- Acquisition is atomic on the foundation side: the worker-request claim and all claim-handle inserts commit together or not at all; cross-boundary consistency comes from orphan reapers on both sides, not two-sided commit. "The cross-boundary invariant is not 'both sides commit together' but 'any orphan state on either side is eventually cleaned up.'" (2026-05-04, foundation-contract, artifact)
- Multi-handle acquisition uses a deterministic sort order shared by all runners — (kind, scope-bytes, purpose) — to prevent cross-runner deadlock (2026-05-04, foundation-contract, artifact).
- Verify-before-run, including the post-commit limb: the runner re-reads claimed_by immediately before invoking a service and bails if ownership moved; a claim stolen between commit and dispatch emits orphaned_claim_lost_race and makes no Execute call (2026-05-04, foundation-contract; 2026-06-02, acceptance-coverage-recovery, artifact).
- Lock state lives only in the foundation persistence layer; service implementations do not persist lock state (2026-05-04, foundation-contract, artifact).
- The foundation reads modeling semantics through exactly four predicates: cascade target, holding-subgraph completion, aggregate outcome, coexistence over byte-equal-scope handles — and nothing else (2026-05-04, modeling-layer-contract, artifact).
- Producer-side cleanup on lifecycle resolutions: pass and error resolutions Abandon each claim already opened Available; retry does not Abandon (producer TTL covers partials); the Unavailable claim itself is never Abandoned — "the producer signaled Unavailable, meaning it has no state to abandon" (2026-05-05, reactive-loops-and-lifecycle-handlers, artifact-only).
- Pre-dispatch acquisition failure folds into the node's error_types via the reserved synthetic class prefix acquire/ (acquire/unavailable); a give_up resolution emits terminal/error/acquire/unavailable so error-wildcard subscribers catch it (2026-05-23, signal-taxonomy-and-policy-decoupling, artifact). Acquisition errors retry in-place on the same node-run row via the exact same applyErrorPolicy machinery as executor errors (2026-06-22, 10cf843b, transcript).
- Fan-out: declared on the parent via fan_out: naming one of its claims; SplitScope returns N sub-scopes; rimsky opens the parent claim plus N sub-claim handles atomically and dispatches N child runs; parent settles after all N reach terminal with an aggregate outcome; no separate fan-out consumer node (2026-05-19, crimefinder; 2026-06-08, corpus-bootstrap, artifact).
- Computed-scope pattern is supported: a producer's Open may compute a view returned as payload bytes, hoisted via {{claim.<alias>.payload.<field>}} substitution (2026-05-19, crimefinder, artifact-only).
- Executors discover producer endpoints through claim address bytes supervisor-provided in ExecuteRequest stores — for declared claims and holds: co-holders alike — not out-of-band config (2026-05-19, crimefinder, artifact-only).
- lifetime: durable is threaded end-to-end (template store-ref → ClaimSpec → producer Open → handle insert, for top-level and fan-out sub-claims), and a durable handle survives holding-subgraph terminal (2026-06-02, rimsky-core-remediation, artifact).
- Durable-committed claims are exempt from trace retention — they are the asset surface (2026-06-03, instance-lifecycle-durable-by-default, artifact-only).
- When a producer advertises SupportsScopesConflict, rimsky consults ScopesConflict during acquisition and in the fan-out sub-claim path, so two writers with overlapping non-byte-equal scopes cannot co-hold (2026-06-06, comprehensive-gap-closure, artifact).
- Operator diagnostics can surface a claim's current holders (alongside parked nodes, wait-set edges, held frames) to diagnose a wedged instance (2026-06-08, corpus-bootstrap, artifact-only).
- SubScopeDescriptor carries address and payload fields; realized_write_semantics and intent are inherited from the parent claim at insert time, not carried on the wire; root claims do NOT gain partition_key or producer_metadata. "parity where it makes sense … a sub-claim is a claim." (2026-06-18, 9fb55f08, transcript)
- Claim-handle payload persists on ALL claim rows (regular and sub-claims) across retries, one uniform code path (2026-06-18, 9fb55f08, transcript).
- Sub-claims have the same intent as their parent claims, propagated into each sub-claim row so the cascade sees correct intent. "why wouldn't we want that to be the case?" (2026-06-18, 8e7e4c10, transcript)
- An intent: r fan-out claim must yield read-only behavior end-to-end on children — enforced at the coexistence layer (STORY-fanout-intent-inheritance, 2026-06-18, 9fb55f08, transcript; enforcement-layer corrected same day, 8e7e4c10).
- ModeCoexists takes (holder intent, candidate intent, one shared write-semantics value); mvccPassThrough is true only for staged_async and panics loudly on unknown enum values (2026-06-19, a02fe167 / 8a3b8c19, transcript).
- Dead-supervisor recovery is quiet-period dispatch-level reaping: SweepExecutorDeadlines releases claims where quiet time exceeds max_quiet_period (or total exceeds max_runtime); re-assignment is the normal pending-queue path (2026-06-21, 21306ffe, transcript).
- Substitution from claims stays; claim substitution is the stale→running round (2026-06-21, 10cf843b, transcript).
- In-place retry: the claim stays held, the executor is re-invoked on the same run with the same claims and bag; only give_up transitions the run to failed (2026-06-22, 10cf843b, transcript).
- Across a holding boundary the fan-out parent is signal-only: non-members subscribe to state signals and get no attribute payload; fan-out child data reaches outsiders via the claim's commit into an external store or a sibling regular node (2026-06-23, 10cf843b, transcript).
- Only an executor can park; a fan-out/aggregation parent with no executor can never be Parked; an acquirer with downstream inheritors does not commit/abandon until its entire holding subgraph resolves (2026-06-24, 8a8539a4, transcript).
- The template directive for claim declarations is claims: per design vocabulary; stores: is the legacy parser spelling of the same directive (2026-05-19, crimefinder, artifact-only — recorded as documented doc-vs-code drift with claims: the intended direction).

## Intentional absences

- No preemption of a parked claim holder — forcing release without consent would contradict park's meaning ("hang on to my claim") (2026-06-16, 055468fc, transcript).
- No operator-intervention carve-out for parked or pending-claim states; the only direct-node-targeting outside the debug channel is the retry-budget clear on failed-terminal rows (2026-06-16, 4c42fe5b, transcript).
- Producer-side intent gating (bundled stores branching Commit/Abandon on intent) — declined as architecturally incorrect work at the wrong layer, not merely out of scope (2026-06-18, 8e7e4c10, transcript).
- Generalizing SplitScope beyond fan_out (e.g. a partition-selector template surface) — rejected; single-partition access is expressible through the producer's selector grammar with substitution (2026-06-19, a02fe167, transcript).
- Removing claim references from the substitution grammar — considered and declined (2026-06-21, 10cf843b, transcript).
- Fresh-row retry, the policy_retry creation reason, the running→failed policy_retry transition, and the held-abort special case — eliminated by the in-place-retry reversal (2026-06-22, 10cf843b, transcript).
- Heartbeat-based supervisor death detection — removed (migration 013); quiet-period is the preferred mechanism (2026-06-21, 21306ffe, transcript).
- TestModeCoexistsCrossQuadrant — deleted as pinning an undefined region; cross-value cells do not exist under byte-equal-scope uniformity (2026-06-19, a02fe167, transcript).
- Special-case held-claim logic on pass resolutions — none exists by design; lazy cascade handles the holding subgraph, and mixed-upstream wakes route through template_resolution_failed error_types (2026-05-05, reactive-loops-and-lifecycle-handlers, artifact-only).
- Removing the parked state (branch feature/nopark) — explicitly declined; park's claim-system signal alone justifies it (2026-06-16, 055468fc, transcript).

## Corrections and restorations (drift-fight record)

- claims: vs stores: — documented doc-vs-code drift: design docs use claims:, shipped parser accepts stores:; design vocabulary is the intended direction (2026-05-19, crimefinder, artifact).
- lifetime: durable silently dropped at acquire (every handle defaulted to subgraph; the "e2e" durable test bypassed the real path) — restored by threading Lifetime through the full acquire path for top-level and sub-claims (2026-06-02, rimsky-core-remediation, artifact).
- ScopesConflict advertised but never consulted (zero callers; acquisition compared only byte-equality) — ruled a gap to close at acquisition and fan-out sub-claim paths (2026-06-06, comprehensive-gap-closure, artifact).
- Hardcoded sub-claim intent of "rw" in AcquireSubClaims ruled "a bug, not a design choice"; sub-claims must inherit parent intent; payload discarded after substitution for regular claims also corrected to persist on all rows (2026-06-18, 9fb55f08, transcript).
- STORY-fanout-intent-inheritance Acceptance demanded producer-side intent gating — corrected: the load-bearing consumer of intent is the coexistence predicate in lib/foundation/locks/conflict.go (2026-06-18, 8e7e4c10, transcript).
- Conflict-gate reshape: ModeCoexists narrowed to a single-semantics signature; isSync renamed mvccPassThrough; silent default-to-true replaced by panic; concept doc rewritten to per-value sub-matrices. "fix it. fix the concept doc, too. fix it all." (2026-06-19, a02fe167, transcript)

## Superseded / historical

- on_acquire_unavailable lifecycle-handler design (Unavailable → fresh/passed, no cascade; default silent retry) (2026-05-05) — superseded by the signal-taxonomy folding of acquisition failure into error_types via acquire/ (2026-05-23) and the unified in-place acquire-retry path (2026-06-22). The durable kernel survives: Unavailable is a clean non-error outcome the author routes, not an executor failure.
- Fresh-row acquire-retry (policy_retry creating a new stale row per retry, from decision non-cascade-direct-to-stale) — superseded by unified in-place retry (2026-06-22, 10cf843b, transcript).
- Message-tied partition_request (backfill via partition_request_override, from the 2026-05-15 data-platform design) — superseded: fan-out has nothing to do with messages; fan-outs must create and resolve within a frame on any supporting producer (2026-06-18, 9fb55f08, transcript).
- Supervisor heartbeat / last_heartbeat_at death detection — superseded by quiet-period reaping (2026-06-21, 21306ffe, transcript).
- stores: as the template directive name — superseded in design intent by claims: (2026-05-19, crimefinder, artifact).

## Conflicts needing human ruling

None recorded — the precedence rules resolve the record's tensions on this concept.
