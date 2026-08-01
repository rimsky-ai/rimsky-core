# Intent Dossier: claim-handle

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- **claim.md owns the layer-split paragraph** (user ruling 2026-07-17): the claim/claim-handle protocol-vs-persistence split is defined once, in the claim concept; claim-handle.md opens with its own ledger-row definition plus a slug cross-reference. Verbatim duplication of the paragraph across the two catalogs was drift risk, not intentional standalone readability.

- claim and claim-handle are layer-distinct nouns for one conceptual thing: claim is the protocol-layer noun returned by ClaimProducer.Open; claim-handle is the rimsky-persistence-layer noun (row in `rimsky_claim_handles`). Invariant 20 (claim content inert) gates the content; invariant 4 (claimant-guarded release) gates the row (2026-05-11, log-convergence, artifact).
- Conflict primitive: two handles conflict iff their claim-scope bytes are byte-equal; the foundation never parses or range-matches scope bytes (2026-05-04, foundation-contract, artifact). All handles with the same (producer, scope-bytes) must have identical realized write-semantics; cross-semantics coexistence is deliberately undefined (2026-06-19, a02fe167, transcript).
- Content (address, payload, realized_write_semantics) is written only inside the acquisition transaction by the producer's Open (invariant 15) and is inert thereafter; the only sanctioned introspection site is the substitution engine's leaf extraction (2026-05-04, artifact).
- State model: explicit `state` enum active/committed/abandoned + resolved_at; exactly three states, no `released`; revival transitions illegal; Promote is claimant-guarded; CHECK constraints force holder_supervisor_id NOT NULL on active rows and NULL otherwise (2026-05-17, post-data-platform-cleanup, artifact).
- Lifetime: subgraph (default — Promote at holding-subgraph terminal, swept after retention) vs durable (exempt from retention sweep, still occupies the scope for conflict detection, released only via asset Release or instance termination) (2026-05-15 + 2026-05-17 + 2026-06-10, artifact).
- Held is a node-run condition derived from claim participation: a run is held while it participates in any unresolved claim handle as acquirer or co-holder — no new column or table; claim resolution walks all holders; each holder leaves held only when its full claim portfolio resolves (all committed → fresh; any abandoned → failed, poison rule); cascade among holders continues during the hold; the deferred commit/abandon cascade fires only to non-members (2026-06-20..2026-06-21, 8a3b8c19/10cf843b, transcript).
- Heartbeats are gone: liveness vocabulary replaces heartbeat; last_heartbeat_at columns dropped (node_runs, supervisors, claim_handles); the expires_at TTL math survives under the liveness name (2026-06-17, b31002b8, transcript).
- A sub-claim is a claim: payload persistence and claimant-guard rules apply uniformly to all claim rows; sub-claims inherit intent and realized_write_semantics from the parent (2026-06-18, 9fb55f08, transcript).
- Single audited resolution engine: ResolveClaimHandleTerminal pairs the producer verb with the claimant-guarded delete/promote in one tx; the acquire-unavailable handler is the single named carve-out (its acquisition tx already rolled back); the periodic reaper fires no producer verb (2026-05-11 artifact; carve-out set narrowed 2026-06-11, last-mile-stability, artifact).
- Terminate vs DELETE: terminate makes the instance terminal and abandons uncommitted in-flight claims; DELETE is the reaper that releases held durable claims and frees instance_key, permitted only once terminal (2026-05-28, quality-of-life, artifact).

## Required behaviors (open promises)

- Byte-equality is the only conflict primitive; canonicalization is the producer's responsibility (2026-05-04, foundation-contract, artifact): "Byte-equality is the *only* conflict primitive."
- Invariant 4: every path deleting a handle or nullifying claimed_by is conditioned on holder = expected; no path removes a holder's row without holder verification (2026-05-04, artifact). The guard predicate is written exactly once per persistence driver via an internal helper, proven by a driver-parity conformance suite where wrong-supervisor mutations change nothing (2026-06-11, last-mile-stability, artifact).
- Invariant 15: address, payload, realized_write_semantics written only via Open inside the acquisition tx — never back-filled or refreshed (2026-05-04, artifact).
- Invariant 20: claim content inert in the foundation; read only at substitution leaf extraction (2026-05-04, artifact).
- Byte-equal-scope serialization guarantees two rw claims on one scope are never open simultaneously; producers need no internal scope coordination (2026-05-14, artifact). Same-(producer, scope) rows have uniform realized write-semantics; do not model, test, or document cross-semantics coexistence (2026-06-19, a02fe167, transcript): "fix it. fix the concept doc, too. fix it all."
- `rimsky_claim_handles.node_run_id` FK keeps ON DELETE SET NULL so held handles outlive the node-run's active terminal (2026-05-04 correction, reaffirmed 2026-05-12, artifact).
- Three-state model with claimant-guarded Promote returning ErrIllegalClaimHandleTransition on zero affected rows; committed→* and abandoned→* forbidden (2026-05-17, artifact).
- Terminal resolution promotes (not deletes); SweepClaimHandleRetention reaps committed-subgraph and abandoned rows past retention (default 30d), serialized by the scheduler-tick advisory lock, and must never delete durable-committed rows — those are the asset surface (2026-05-17, artifact). Note recorded gap: no YAML wiring for retention.claim_handles_trailing landed (artifact-only divergence, 2026-05-17).
- Durable lifetime end-to-end: lifetime threads from the template store-ref through ClaimSpec, Open, and the insert for both top-level and fan-out sub-claims; a durable handle survives holding-subgraph terminal, participates in conflict detection while committed, is readable via `holds:` in the same instance without re-acquiring, and is released only via the asset Release endpoint or instance termination (2026-05-15 + 2026-06-02 + 2026-06-10, artifact).
- Orphan-claim reaper candidate predicate is state='active' AND expired; committed/abandoned rows are structurally never candidates (2026-05-17, artifact).
- Non-active-row deletions route through absence-guarded DeleteResolved (state committed/abandoned AND holder NULL) for both held-durable release and asset DELETE (2026-05-17, artifact).
- Payload is persisted on ALL claim rows (regular and sub-claims) for one uniform code path; payload survives retries and feeds co-holder `{{claim.<alias>.payload.*}}` substitution past the acquirer's tx (2026-06-10 artifact; ratified ALL-rows 2026-06-18, 9fb55f08, transcript): "yes and yes to 'My lean: ALL rows'".
- Sub-claims inherit the parent claim's declared intent — the hardcoded "rw" was a bug, not a design choice; SubScopeDescriptor carries address and payload; root claims do NOT gain partition_key/producer_metadata (2026-06-18, 9fb55f08, transcript).
- Base-protocol Commit response fields are real: version_id persisted to the row from base Commit, producer_metadata surfaced in the fan-out parent's writeback (2026-06-11, last-mile-stability, artifact).
- Held mechanism (the governing transcript model, 2026-06-20..21, 8a3b8c19/10cf843b): held derives from unresolved claim participation; CheckAndFireResolution walks all holders on claim resolution; portfolio rule held→fresh (all committed) / held→failed (any abandoned); cascade filtered to holding-subgraph members during the hold; deferred cascade at commit/abandon fires only to non-members (never to members — would double-fire); gate evaluator's upstream-in-flight check skips held co-members. "ok, so the claim resolving triggers a reevalution of all nodes holding on it."
- Poison rule with forward propagation: any holder failure drives every participating holder to failed via auto_terminal_abandon at resolution, including in-flight holders that settle later regardless of their own verdict (2026-06-21, 10cf843b, transcript). On member failure the acquirer auto-transitions to Failed with terminal/error/abandoned; staged changes stay un-swapped (2026-06-29, 8a8539a4, transcript).
- A held run never receives an error: held is post-executor, idle, waiting for commit/abandon; errors happen while running (2026-06-22, 10cf843b, transcript). With in-place retry there is no held probe; retry exhaustion routes give_up, whose success=false release poisons the claim (2026-06-22, transcript).
- A fan-out parent transitions running→held immediately after dispatching partition children, with dedicated reason fanout_dispatched (not handler_held), releasing its queue claim; resolution unifies onto the same auto-terminal walker (2026-06-22, 10cf843b, transcript).
- A queued successor behind a held predecessor waits for the full held-claim release (auto-terminal commit/abandon, not executor return) and then acquires its own claims fresh — no claim inheritance; the unique in-flight constraint on pending rows is gone (2026-06-20, 8a3b8c19, transcript).
- Claim held across a park: the claim acquired at first dispatch persists through parked and auto-releases only on a true terminal verdict (2026-05-08, platform-extensions, artifact).
- holds:-only templates engage the held auto-terminal path end-to-end: commit on all-success, abandon on any-failure (2026-06-02, rimsky-core-remediation, artifact).
- A settling signal's attributes_delta is what the dispatch's verdict contributed — a passive holder auto-terminating from portfolio resolution carries an empty delta, never the full bag (2026-06-24, 8a8539a4, transcript).
- Invariant 10 (parent-claim acquisition atomicity): the acquisition tx claims the parent run AND inserts parent + all sub-claim handle rows AND records Open addresses, or none (2026-05-15, artifact).
- Holders endpoint returns 200 with `{"holders": []}` when empty, not 404 (2026-05-04, artifact-only).
- Forensics: within the retention window, rimsky_claim_handles answers "what claims existed and how did they resolve" without lineage joins; lineage is walkable/queryable by claim handle (2026-05-17 + 2026-06-08, artifact).

## Intentional absences

- **A `released` state** — the 3-state model is locked; post-Release the row is deleted outright (2026-05-17, post-data-platform-cleanup, artifact).
- **A separate rimsky_assets table** — assets are committed durable claim-handle rows (Option D articulated and rejected) (2026-05-17, artifact).
- **held_durable bool** — replaced by the state enum + lifetime column (2026-05-17, artifact).
- **Heartbeat machinery** — last_heartbeat_at columns, doHeartbeat/HeartbeatInterval vocabulary, SweepStaleHeartbeats, the 5x-heartbeat_interval cutoff (old blessed-invariant 6), and the deprecated HeartbeatTimeout compat field: all removed for the liveness model (max_runtime / max_quiet_period) (2026-06-17, b31002b8, transcript).
- **Claim inheritance by queued successors** — a successor always acquires fresh through the normal path (2026-06-20, 8a3b8c19, transcript).
- **Cross-semantics coexistence as a defined case** — must not be modeled, tested, or documented (2026-06-19, a02fe167, transcript).
- **The store_name noun** — fully retired from wire, schema, Go fields, and the browse endpoint in favor of producer_name (2026-05-13, nomenclature-resolution, artifact).
- **held-claim as a standalone concept** — folded into claim-handle (held variant) (2026-05-12, nomenclature-resolution, artifact).
- **applyHeldAbort / the held probe, and creation reasons policy_retry / infra_reenqueue, and prior_dispatch back-pointer fields** — removed by the in-place-retry unification (2026-06-22, 10cf843b, transcript).
- **Claimant guard on Release-path deletion** — deliberately absent: post-Promote holder is NULL and the releasing supervisor may not be the acquirer; absence-guarded DeleteResolved is the sanctioned shape (2026-05-17, artifact).
- **ReleaseHeldDurableClaims from terminate** — terminate abandons only force-failed runs' uncommitted in-flight claims; durable release stays DELETE's job (2026-05-28, artifact).

## Corrections and restorations (drift-fight record)

- FK semantics: the plan's ON DELETE CASCADE on the node-run FK cascade-deleted held handles before auto-terminal could fire Commit, sticking producer items in_progress; corrected to SET NULL (2026-05-04, layer-crystallization, artifact).
- failOverdueParkedRow deleted the run row without abandoning held claims (invariant-13 violation); fixed to mark holder rows failed and fire per-handle resolution (2026-05-08, artifact).
- ListByProducerScope predicate: spec's state IN ('active','committed') let committed-subgraph rows block scope reacquisition for the whole retention window; refined to active OR committed-durable (2026-05-17, artifact).
- CountByNamedLock flipped from expires_at to state='active': resolved named-lock rows must not count toward the limit (2026-05-17, artifact).
- holds:-only templates never engaged held auto-terminal (detection walked only Inherits); fixed and proven end-to-end (2026-06-02, rimsky-core-remediation, artifact).
- lifetime: durable silently dropped at acquire (never threaded into the insert; the "e2e" test bypassed the real path); fixed with real threading for top-level and sub-claims (2026-06-02, artifact).
- candidate_handle never reached the fan-out leaf, and the supervisor never dialed DataProcessing producers at all; both wired so BeginCandidate/Commit/AbandonCandidate run in a real stack (2026-06-02, artifact).
- Heartbeat retirement stopped early (doHeartbeat ticking against a dropped column): the user ruled this implementation drift, not an open question — "asked and answered in the spec" (2026-06-17, b31002b8, transcript).
- Held-cascade model: the sketch's blanket "defer all cascade from held until commit/abandon" was corrected — commit/abandon happens after group execution, so holders within the claim scope must keep cascading during the hold (2026-06-20, 8a8539a4→8a3b8c19 era, transcript): "if that is correct, then the sketch is wrong."
- The hardcoded sub-claim intent "rw" ruled a bug (2026-06-18, transcript).
- Discarded base Commit response body ruled a gap and made real (2026-06-11, artifact).
- Passive-aggregator settlement shipping the full attribute bag as attributes_delta ruled wrong and fixed (2026-06-24, transcript).

## Superseded / historical

- is_held flag + in-memory HeldSubgraphs branching (2026-05-04) → held derived from claim participation via claim_holders/claim_handles; portfolio-based transitions (2026-06-20..21).
- held_durable bool (2026-05-15) → state enum + lifetime (2026-05-17).
- Three Abandon-on-opened-claim carve-outs (pre-dispatch, verify-before-run bail, unified engine) (2026-05-11) → verify-before-run bail folded into the engine as OwnershipBail; acquire-unavailable remains the single named carve-out (2026-06-11).
- 5x-heartbeat_interval shared orphan cutoff / blessed-invariant 6 (2026-05-12) → liveness model, max_runtime / max_quiet_period (2026-06-17).
- Unique in-flight constraint blocking pending coexistence → removed for the queue model (2026-06-20).
- "Cascade must not continue past held until resolved" (initial 2026-06-20 framing) → member-internal cascade continues; only non-member cascade defers to commit/abandon (2026-06-20 correction).
- store_name audit-field preservation divergence → 2026-05-13 producer_name sweep.
- LockKindScope constant keeps its Go name with value "claim_scope" — intentional asymmetry from the ClaimScope rename (2026-05-22, artifact).

## Conflicts needing human ruling

- None. The record's apparent held-cascade contradiction (2026-06-20) resolves within the session via the user's own correction; the heartbeat and carve-out changes resolve by later-supersedes-earlier.
