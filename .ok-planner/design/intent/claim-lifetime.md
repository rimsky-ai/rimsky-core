# Intent Dossier: claim-lifetime

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- A claim's `lifetime` is one of **subgraph** (default) or **durable**. Subgraph-lifetime handles are released/deleted at holding-subgraph completion; durable handles are promoted to state `committed` at holding-subgraph completion and persist — released only by explicit operator action (asset DELETE) or instance termination.
- Lifetime is a **row-disposition** choice, never a resolution-outcome choice: durable-Commit promotion and non-durable deletion both bump the parent's outcome counters identically.
- A held claim's lifetime is governed by the **holding subgraph**, not by any frame; auto-terminal fires the producer verb (Commit on success / Abandon on failure) only when every co-holder has settled.
- The claim-handle state model is locked at exactly three states; there is no `released` state — after producer Release the row is deleted outright.
- Committed-durable rows participate in conflict detection (they are the asset surface), are exempt from the retention sweep, and are skipped by the orphan reaper.
- The foundation never calls producer verbs during orphan reap; producer-side TTL owns producer-state cleanup (invariant 6).

## Required behaviors (open promises)

- `lifetime: durable` promotes the claim-handle row to `committed` at holding-subgraph completion, exempts it from the retention sweep, keeps it in conflict detection, and lets future dispatches in the same instance `holds:` the alias and read the persisted bytes without re-acquiring; release only via the asset Release endpoint or instance termination (2026-05-15, data-platform-extensions; restated 2026-06-10, cascade-and-claim-handoff, artifact): "`lifetime: durable` … promoted to state committed at holding-subgraph completion AND … exempted from the retention sweep."
- Lifetime must be threaded end-to-end from the template store-ref through ClaimSpec, producer Open, and the handle insert — for both top-level claims and fan-out sub-claims — and a durable handle must survive holding-subgraph terminal via the real acquire path (2026-06-02, rimsky-core-remediation, artifact; correction of the silent-drop bug).
- The retention sweep never deletes rows with `state='committed' AND lifetime='durable'` (2026-05-17, post-data-platform-cleanup, artifact): "those are the asset surface."
- The orphan-claim reaper skips durable rows (heartbeat pauses on durable claims; without the skip the reaper would delete them once the heartbeat lapsed) (2026-05-15, data-platform-extensions, artifact).
- Foundation orphan reap reverts active-phase worker-requests to pending with claimant-guarded `claimed_by` nullification, never reaps held-phase rows at the worker-request level, and never calls Commit/Abandon/Release (2026-05-04, foundation-contract, artifact-only).
- Both row-disposition branches (durable promote / non-durable delete) bump the parent's outcome counter and recurse the parent walk — a fan-out parent under best_effort whose children all resolve durable-Commit must compute Commit, not Abandon (2026-05-15, data-platform-extensions plan notes, artifact; bug fix made durable).
- Subgraph-lifetime co-held claims auto-commit at subgraph completion: when the last co-holder's run goes non-active, auto-terminal fires Commit (2026-05-19, crimefinder, artifact).
- The handle row stays active across frame boundaries for however many frames the holding subgraph spans, and `{{claim.<alias>.*}}` substitutions resolve to the same bytes in any frame where the subgraph is open; auto-terminal must not fire on the acquirer's settlement alone (2026-06-10, cascade-and-claim-handoff, artifact).
- Conflict routing: persistent conflicts (committed-durable rows) route to acquire/unavailable so `error_types` fires; in-flight-holder conflicts keep retry-then-bail (2026-06-10, completion report, artifact).
- At instance termination rimsky Releases each held-durable handle sequentially; failures are logged and do not block termination (2026-05-15, data-platform-extensions, artifact-only).
- Instance Terminate (force-fail) abandons only the force-failed runs' uncommitted in-flight handles (claimant-guarded Promote to abandoned) and deliberately does NOT release held-durable claims — committed-durable release and instance_key freeing stay DELETE's job; abandon failures are WARN-logged and non-fatal (2026-05-28, quality-of-life-features, artifact).
- `rimsky_claim_handle.worker_request_id` FK is ON DELETE SET NULL so held handles outlive the worker-request's active terminal until auto-terminal resolves them (2026-05-04, layer-crystallization, artifact; the CASCADE draft cascade-deleted held handles before Commit could fire, sticking producer items in_progress).
- The conformance claim-producer suite drives the full lifecycle — Open, Commit, Abandon, Release on real claims, plus a re-issued terminal verb asserting idempotency — each as its own pass/fail row (2026-06-06, comprehensive-gap-closure, artifact).
- Rimsky drives producer verbs at fixed lifecycle points: Commit at auto-terminal success, Abandon on failure, Release at lifecycle close (2026-06-08, corpus-bootstrap, artifact).
- Claims form a tree via `parent_claim_handle_id`; a claim's abandon recursively force-abandons descendants top-down; under strict with cancel_siblings one child's abandon force-abandons in-flight siblings while the failing child's own abandon stays natural/direct (2026-06-19, 08d65bfe, transcript).

## Intentional absences

- **A `released` claim-handle state** — deliberately excluded; the 3-state model is locked and post-Release rows are deleted via the existing Delete path, not a Promote variant (2026-05-17, post-data-platform-cleanup).
- **Foundation-driven producer cleanup during orphan reap** — by design the foundation never calls Commit/Abandon/Release there (2026-05-04).
- **Held-durable release inside Terminate/force-fail** — deliberately withheld; DELETE owns it (2026-05-28).
- **A claim-lifetime home for message-dedup retention** — the message-TTL sweep was judged neither a claim-lifetime nor an orphan-reaper concern; its retention annotations were removed rather than mispointed, leaving it intentionally outside this concept (2026-05-25, concept-doc-self-containment).
- **`phase='completed'` as a lived state** — the schema CHECK accepts it but no code path emits it; Queue.Complete DELETEs at active terminal and callers treat "no row" as the deleted-state signal. Emitting `completed` was logged as a follow-up, never delivered (2026-05-04, layer-crystallization plan notes, artifact-only) — absence is recorded, not promised-and-missing.

## Corrections and restorations (drift-fight record)

- **`lifetime: durable` silently dropped at acquire** (2026-06-02, rimsky-core-remediation): the acquire path never threaded Lifetime into the insert, so every handle defaulted to subgraph, and the "e2e" durable test inserted a durable row directly, bypassing the real path. Ruled: code drift; thread it end-to-end and test through `acquireClaim`.
- **ON DELETE CASCADE FK** (2026-05-04, layer-crystallization): plan spec was wrong, found by smoke test; SET NULL restored the held-claim mechanism.
- **Durable-Commit aggregation miscount** (2026-05-15, plan notes): durable promotion skipped the parent counter bump; fixed by separating row-disposition from resolution-outcome.
- **Conformance suite under-coverage + false docstring** (2026-06-06): suite didn't drive Commit/Abandon/Release; the CLI docstring already claimed "the four runtime verbs" — code fixed to match the claim.

## Superseded / historical

- Spec's `phase` lifecycle including a lived `completed` state → execution kept DELETE-at-terminal (2026-05-04 divergence).
- `held_durable: true` boolean flag modeling (2026-05-15) → the state-column model `state='committed' AND lifetime='durable'` (2026-05-17); behavior explicitly unchanged by the refactor.
