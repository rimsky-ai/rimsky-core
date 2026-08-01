# Intent Dossier: terminal-resolution

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- "Terminal" means run-terminal only: a signal that can be followed by another dispatch in the same run is not a terminal. The signal taxonomy collapses terminal/* to exactly `terminal/success` and `terminal/error/<class>`; park is `transient/park/snooze` / `transient/park/await_callback` (audit-only, never cascade-fires, subscriptions rejected at registration); `terminal/infra/*` is deleted — infra failures retry via transient/infra and settle as terminal/error/<infra-class> (2026-06-24, 8a8539a4, transcript, user: "a 'terminal' that can cause another dispatch in the same run is not a terminal").
- Every executor settling terminal carries one uniform shape: attributes_delta + tags + scratch. Invariant terminal-atomic-commit: the settling verdict, the attributes_delta writeback, and tags persistence ride the same caller-provided transaction and commit together (2026-06-17, b31002b8, transcript).
- Every terminal outcome continues the cascade — the cascade engine is signal-blind; terminal/error cascades exactly like terminal/success. Only park, infra, and await_async are audit-only, because the node resumes rather than settling (2026-06-18, 8a3b8c19, transcript, user).
- The terminal decision is a closed flat sum: TerminalOutcome ∈ {Commit, Abandon, AbandonSiblingCancel, AbandonDescendantCancel} with IsAbandon()/CauseString() helpers; wire-level cause strings preserved for lineage/event consumers (2026-06-19, 08d65bfe + 8a3b8c19, transcripts).
- Claim-handle resolution is single, terminal, and aggregate-outcome-driven (foundation invariant 13): lock the row, evaluate aggregate outcome, fire exactly one producer verb, then claimant-guarded delete — never partial, first-delete-wins, or last-released-wins (2026-05-04, foundation-contract, artifact). `ResolveClaimHandleTerminal` is the single audited verb-then-delete engine; the producer verb must succeed before the row is deleted (2026-05-04, layer-crystallization, artifact).
- The acquire-unavailable handler is the single named carve-out outside the engine (its acquisition tx already rolled back, so there is no claimant-guarded delete to fold) (2026-06-11, last-mile-stability, artifact).
- Callback determinism is a blessed invariant: a callback is honored iff the run's phase is active or held at processing time, checked atomically in the same tx as the state mutation; anything else is ack-but-noop. The original TOCTOU gap was closed when applyTerminal was rewritten to accept the outer tx (2026-05-22 artifact; closure confirmed 2026-06-06, comprehensive-gap-closure, artifact).
- Attribute-changed signal generation at settle is uniform: every node, on settling, regardless of node type, executor presence, terminal kind, or code path, emits attribute-changed signals based solely on the persisted attribute delta (2026-06-22, 10cf843b, transcript, user).
- A frame cannot fail if all its nodes settled: frame status is a pure function of node_run states; there is no independent frame-level failure concept (2026-07-06, 3f71f90a, transcript, user).
- Node execution is orderly and stepwise from the graph's perspective: a node fully settles at its terminal before any downstream subscriber sees its signals or dispatches (2026-06-16, 055468fc, transcript, user).

## Required behaviors (open promises)

- All three settling outcomes (Success, Error, Park) carry attributes_delta and the runtime actually commits the delta for every variant — Error via applyTerminalError, Park via applyTerminalPark, inside the caller-provided transaction (2026-06-17, b31002b8, transcript, user: "right. fix everything. implement every missing thing. we want 100%.").
- The async callback is an HTTP POST of AsyncCallbackBody to `${callback_url}/v1/callback/{async_ack_id}` with exactly one of success | error | park (2026-06-08, corpus-bootstrap, artifact; the optional `events` array was removed by the 2026-06-16 executor-protocol-coherence lock, 055468fc, transcript). The async-callback registry is persistent so restart recovery works (2026-06-16/17, transcripts).
- Claim-tree aggregation is counter-driven and order-independent: the aggregation policy snapshots onto the parent handle with expected/committed/abandoned counters; strict: any abandoned aborts; threshold: abandoned>max aborts; best_effort/first: any commit promotes; unknown defaults strict (2026-05-16, data-platform-extensions, artifact-only). Row disposition (durable-Commit vs delete) is never conflated with resolution outcome — both branches bump counters and recurse (2026-05-16, artifact-only).
- A held parent claim defers its verb until all co-holders are done; the last holder's transition re-drives resolution (2026-05-16, artifact-only). strict.cancel_siblings force-abandons in-flight siblings and cancels descendants before each row's own delete so the FK chain never orphans (2026-05-16, artifact-only; sibling/descendant-cancel semantics reconfirmed 2026-06-19, 08d65bfe, transcript).
- The held-cascade rule: every node that executed during a claim hold broadcasts the outcome to its subscribers at resolution — terminal/success at commit, terminal/error/abandoned at abandon; during the hold, cascade flows only among held-subgraph members; non-members see the terminal signal only at resolution; nodes that never executed broadcast nothing (2026-06-20, 8a3b8c19, transcript, user).
- When a held-subgraph member fails, the holder/acquirer auto-transitions to Failed with terminal/error/abandoned — abandon is a holder-failure state, not a separate lock phase — while staged changes stay un-swapped (2026-06-29, 8a8539a4, transcript).
- Across a holding boundary the fan-out parent is signal-only: non-members subscribe to state signals and get no attribute payload; substitution stops at the boundary; child data must be materialized by the commit or written to a sibling owner node (2026-06-23, 10cf843b, transcript, user).
- The verify-before-run ownership-bail (handleOrphanedClaim) resolves through the unified engine as source kind OwnershipBail; the hand-rolled verb-then-delete site is deleted; the periodic reaper fires no producer verb; the acquire-unavailable carve-out is pinned by a deterministic injection test (2026-06-11, last-mile-stability, artifact).
- Give-up on pre-dispatch acquisition failure emits terminal/error/acquire/unavailable, namespaced under terminal/error/* so wildcard error subscribers catch runtime failures alongside executor failures (2026-05-23, signal-taxonomy, artifact).
- Infrastructure-class errors intentionally skip operator-declared error policy (2026-07-13, 3f71f90a, transcript; adjudicated fix-doc, finding 54 — error-policy.md was the stale side).
- The claude-agent sign-off gate guards success only: a terminal error fires terminal/error/<class>, never terminal/success; report_error stays available as the honest-failure exit (2026-06-04, claude-agent-signoff-gate, artifact-only).
- Message emission by an executor is not part of the terminal-resolution transaction and must not be: it is an executor work product, idempotency makes retries safe, and a sent message is not cancelled on rollback (2026-06-19, 8a3b8c19, transcript, user).
- compose run drives every declared instance to instance-terminal then exits, with exit codes 0 all-success / 1 any-failure (including park_timeout) / 2 wall-clock timeout / 130 interrupt; --timeout is opt-in with no default; parked nodes need no special handling because park's exits drive instances to terminal (2026-06-13/14, 65667e33 / f0176bde, transcripts).
- Terminal-resolution keeps bare Registry.Get (no late-bind resolution) on the recursive walk: the parent claim was bound at acquire time (2026-05-24, host-agent-and-proxy, artifact-only).
- Every work_started pairs with a work_completed at terminal application; parked / await-async re-entry excluded — park is in-flight and the eventual terminal emits the pair (2026-06-11 artifact; 2026-07-13, 3f71f90a, transcript, finding 2348 fix-doc).

## Intentional absences

- **Per-verb deadline on producer Commit/Abandon calls.** A 30-60s context timeout in ResolveClaimHandleTerminal was recommended as a defensive fix but deliberately not landed with the crystallization work (2026-05-04, layer-crystallization, artifact-only). Its absence is a recorded deliberate deferral, not unnoticed drift.
- **terminal/infra/* signal family.** Deleted — it was never emitted (2026-06-24, 8a8539a4, transcript).
- **Park as a settling terminal.** Park moved to transient/park/*; subscriptions to it are rejected at registration (2026-06-24, transcript). AwaitAsyncCallback was already established as not-a-park and not-a-settling-terminal — the node stays running (2026-05-23, artifact).
- **"Terminal" as a wire-protocol term.** Retired at the wire; retains only its state-machine / decision-engine meaning (2026-05-12, nomenclature-resolution, artifact). The streaming wire surface itself (StreamClose) was subsequently retired by the unary-Execute reshape (2026-06-16, 055468fc, transcript).
- **The Outcome × Cause product type** (and the Natural placeholder) — replaced by the flat TerminalOutcome enum (2026-06-19, 08d65bfe, transcript).
- **Guaranteed terminal event per dispatch.** Late/stale callbacks are dropped by determinism; consumers tolerate gaps (2026-05-22, artifact).
- **The `Blocked` legacy outcome and the {type: complete|blocked|errored} callback discriminator.** The May guidance on Blocked-as-routing-signal (2026-05-08, artifact) is void: the wire shape is Success | Error | Park | AwaitAsyncCallback (2026-05-23 correction, artifact; 2026-06-16 reshape).
- **Auto-instance-termination.** In the retired-mechanisms list (2026-07-07, 3f71f90a, transcript) along with terminate_after_run; earlier behaviors keyed to MarkInstanceTerminatedIfDone (e.g. the 2026-06-02 publisher-subscription NOT-EXISTS guard) describe a superseded termination model — do not restore them as-is.

## Corrections and restorations (drift-fight record)

- Round-1 validation caught silently dropped design: applyTerminalError/applyTerminalPark discarded attributes_delta, RegisterAsyncAck was never called so restart recovery 404ed, max_runtime unenforced, max_quiet_period wrong default. User mandated zero scope cuts; round-2 confirmed all 13 TDs delivered (2026-06-17, b31002b8, transcript).
- Recursive claim-tree resolution originally decided the parent verdict from the last child alone (order-dependent); fixed with snapshot policy + counters (2026-05-16, artifact). Durable-Commit rows failed to bump the committed counter, making best_effort parents Abandon wrongly; fixed (2026-05-16, artifact).
- Held parents fired their verb while co-holders were still active; fixed to defer until the last holder settles (2026-05-16, artifact).
- The callback-determinism invariant was annotated while only partially implemented (TOCTOU window, deferred refactor) (2026-05-22, divergences, artifact) — later genuinely closed by passing the outer tx into applyTerminal (2026-06-06, artifact). Precedent: an invariant annotation is not proof of implementation.
- Concept docs called the third wire outcome "Snooze"; corrected to Park with ParkReason (2026-05-23, artifact) — itself later superseded by the 2026-06-24 park-is-transient ruling.
- The tension abandon-on-pass-duplicated-path over-stated drift: applyTerminalPass was wrongly listed as bypassing the engine when it routes through releaseLocksInTx (2026-05-11, artifact). Precedent: verify a claimed bypass before ruling code drift.
- Cascade-on-every-terminal has been re-litigated repeatedly; the user cited the story record to stop regression re-introductions (2026-06-18, 8a3b8c19, transcript). Findings that expect terminal/error not to cascade assume drifted expectations.

## Superseded / historical

- last_outcome flavor field and the `last_outcome == fresh_changed` cascade gate (2026-05-04, foundation-contract) → cascade-fire is subscriber match (2026-05-23); fresh_changed is in the retired-mechanisms list (2026-07-11).
- ParkRequested as a fifth terminal event with parked joining the node states (2026-05-08) → Park with ParkReason (2026-05-23) → park reclassified transient, not terminal (2026-06-24).
- The three-carve-out Abandon taxonomy (pre-dispatch, verify-before-run bail, unified engine — 2026-05-11) → bail path folded into the engine as OwnershipBail; acquire-unavailable remains the single carve-out (2026-06-11).
- Outcome × Cause product type → flat TerminalOutcome enum (2026-06-19).
- AsyncCallbackBody with optional events[] (2026-06-08) → events removed with the named-event retirement (2026-06-16).
- Blocked-as-self-block routing guidance (2026-05-08) → outcome vocabulary without Blocked (2026-05-23 onward).
- The foundation's any-(auto_recovers, cascade_targets)-pair failure terminal with the three-action modeling vocabulary (2026-05-04) → the Resolution tuple decoupling signal / disposition / color, where color is informational and cascade never gates on it (2026-05-23).

## Conflicts needing human ruling

- None left open by precedence. (The per-verb producer deadline question was RESOLVED 2026-07-14 by user ruling — superseded by the durable ordered per-scope terminal-verb outbox; see the claim-producer dossier for the full design. The May-04 semantic timeout is dead; per-attempt deadlines are connection hygiene only.)
