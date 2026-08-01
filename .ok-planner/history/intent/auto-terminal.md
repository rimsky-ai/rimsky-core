# Intent Dossier: auto-terminal

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Auto-terminal is the held-claim Commit/Abandon resolution mechanism: when every holder of a claim handle (acquirer plus co-holders) has settled non-active, exactly one automatic resolution fires — Commit on all-success, Abandon on any-failure. Resolution is single, terminal, and aggregate-outcome-driven; never partial, never first-delete-wins.
- Held is a derived node-run condition, not a new column or table: a run is held while it participates in any unresolved claim handle as acquirer or co-holder; membership derives from the existing claim_holders / claim_handles tables (option B, 2026-06-20).
- Cascade past a held node is deferred until the claim resolves; cascade among holders inside the claim scope continues during the hold. The deferred Commit/Abandon cascade fires only to non-members of the holding subgraph.
- Claim resolution triggers re-evaluation of all holders (CheckAndFireResolution walks them): a holder leaves held only when its full claim portfolio is resolved — held-to-fresh if all committed, held-to-failed if any abandoned (poison rule, with forward propagation to holders that settle later).
- The producer verb fires before the claim-handle row mutation, inside the unified terminal-resolution engine; the row is never deleted/promoted without the verb having succeeded.
- A held claim's lifetime is governed by the holding subgraph, not by any frame; auto-terminal must not fire on the acquirer's settlement alone.
- The name "auto-terminal" is internal-only (not on public surfaces) and is reserved for this mechanism — it must not be reused for other "auto" features.

## Required behaviors (open promises)

- Exactly one aggregate-outcome resolution per held claim: lock the claim-handle row, evaluate aggregate outcome (any-failed → Abandon; all-completed → Commit), fire exactly one terminal verb, then claimant-guarded row cleanup (2026-05-04, foundation-contract, artifact): "Resolution is single, terminal, and aggregate-outcome-driven."
- Producer verb before row mutation, in one shared engine (ResolveClaimHandleTerminal): no path where the claim-handle row is removed without the producer verb succeeding first (2026-05-04, layer-crystallization, artifact): "only deletes the claim_handle row AFTER the producer verb (Commit/Abandon) succeeds."
- `holds: { <alias>: { from: <upstream> } }` co-holdership works end-to-end: co-holders extend the holding subgraph, resolve `{{claim.<alias>.*}}` to the same bytes the acquirer received, and auto-terminal fires only when all holder rows are non-active (2026-05-15, data-platform-extensions, artifact; re-promised 2026-06-10, cascade-and-claim-handoff): "Auto-terminal fires once every node in the holding subgraph settles non-active: Commit on all-success, Abandon on any-failed." Includes holds:-only templates (no inherits) — proven end-to-end after the 2026-06-02 remediation.
- Premature-firing guard: when no holder rows are active, resolution consults the template's expected holding-subgraph member set and refuses to fire while an expected member has not yet inserted its holder row — except on anyFailed, where Abandon fires immediately (2026-05-15, data-platform-extensions, artifact) `(artifact-only)`.
- A parent claim handle that is itself held defers its verb until all its co-holders are done; the recursive walk returns without firing if any holder row is still active and re-drives on the last transition (2026-05-16, data-platform-extensions, artifact) `(artifact-only)`.
- Held subgraphs commit-or-abandon atomically at the producer: aggregate success atomically swaps staged data into the canonical view; any aggregate failure drops staging (2026-06-02, acceptance-coverage-recovery, artifact) `(artifact-only)` — note the filesystem store itself was later ruled sync-only (see write-semantics dossier).
- Cascade defers at held: the terminal cascade walk is deferred until auto-terminal commit (fires terminal/success) or abandon (fires terminal/error/abandoned); downstream must never act on provisional held data with no retract path (2026-06-20, 8a3b8c19, transcript): "cascade can't continue at all until held is resolved."
- `terminal/error/abandoned` is a cascade-firable, subscribable signal meaning the node's work was lost; the graph must be able to detect and react (2026-06-20, 8a3b8c19, transcript): "it means the node's work was lost, so the graph should be able to detect and react to that."
- Poison rule with forward propagation: any holder failure drives every participating holder to failed via auto_terminal_abandon at resolution, including in-flight holders that settle later regardless of their own verdict (2026-06-21, 10cf843b, transcript).
- Deferred Commit/Abandon cascade fires only to non-members of the holding subgraph; members coordinate via the held state transition and get no downstream cascade (double-fire forbidden) (2026-06-21, 10cf843b, transcript).
- A queued successor run behind a held predecessor waits for the full held-claim release (auto-terminal commit/abandon, not executor return) and acquires its own claims fresh on dispatch — no claim inheritance (2026-06-20, 8a3b8c19, transcript).
- A held-participating run that exhausts in-place retries routes through give_up, whose lock release with success=false poisons the claim and auto-terminal abandons all holders (2026-06-22, 10cf843b, transcript).
- The gate evaluator's upstream-in-flight check skips held upstreams that are co-members of the receiver's held subgraph (2026-06-20, 8a3b8c19, transcript).
- pg/swap_failed surfaces as a classed error at the gRPC terminal-verb boundary, routed into the holder's error_types — never as a tx-fatal error that would wedge the auto-terminal transaction (2026-06-06, comprehensive-gap-closure, artifact) `(artifact-only)`.
- The 2026-06-20 redesign's story set is test-guarded: cascade-defers-during-flight, held-commit-cascades-success, held-abandon-cascades-abandoned (among the seven), each with a scenario proof (2026-06-20, 8a3b8c19, transcript).

## Intentional absences

- No `auto_terminate` naming on the instance lifecycle flag: the flag is `terminate_after_run`, precisely to avoid overloading the auto-terminal word (2026-06-03, instance-lifecycle-durable-by-default, artifact).
- No any-of / first-fresh-of-set runtime-config feature: eligibility-as-empty-wait-set IS the any-of semantic (2026-05-14, subscription-cascade-and-quality-of-life, artifact).
- No unique in-flight constraint on pending rows: retired so queued runs can coexist behind a held predecessor (2026-06-20, 8a3b8c19, transcript).
- No held probe / applyHeldAbort special-case in error handling: retired with in-place retry; give_up + poison is the uniform path. The prior_dispatch back-pointer fields and policy_retry/infra_reenqueue creation reasons are dropped (2026-06-22, 10cf843b, transcript).
- No cascade from held to ALL downstream subscribers deferred until commit/abandon (the sketch's blanket rule): corrected — intra-subgraph cascade continues during hold (2026-06-20, 8a3b8c19, transcript).
- "auto-terminal" as a public-surface term: deliberately absent; public docs describe held-claim resolution in plain terms (2026-05-04, public-docs-architecture, artifact).
- The OnAcquireUnavailable pre-dispatch carve-out deliberately does NOT route through the unified engine (claim-handle rows already rolled back; it Abandons already-Open'd claims fail-soft) — it is the single named carve-out (2026-05-11, log-convergence; reaffirmed 2026-06-11 last-mile-stability, artifact).
- Heartbeat-based liveness feeding auto-terminal timing: removed entirely (2026-06-17, b31002b8, transcript); claim-handle expires_at TTL math survives under the liveness name.

## Corrections and restorations (drift-fight record)

- ON DELETE CASCADE on rimsky_claim_handle.worker_request_id cascade-deleted held handles before auto-terminal could fire Commit, leaving producer items stuck in_progress; ruled a bug, fixed to SET NULL (2026-05-04, layer-crystallization, artifact).
- The tension abandon-on-pass-duplicated-path overstated drift: applyTerminalPass was wrongly listed as bypassing the unified engine; only handleAcquireUnavailable.abandonPartialLocks was genuinely duplicated (2026-05-11, log-convergence, artifact).
- holds:-only templates never engaged the held path (detection layer walked only Inherits): documented co-held Commit/Abandon never fired; fixed and proven end-to-end with Commit on all-success and Abandon on any-failure (2026-06-02, rimsky-core-remediation, artifact).
- The sketch's blanket "defer all cascade from held" model was corrected by the user: commit/abandon happens after group execution, so holders within a claim scope must continue to cascade among themselves (2026-06-20, 8a3b8c19, transcript).
- The atomic-staging terminal-routing contract (verifier_failed → Abandon, Success → Commit) was left asserted by shape only, never exercised end-to-end — a known execution divergence, not a design retraction (2026-05-19, multi-instance-template-ergonomics, artifact).

## Superseded / historical

- Heartbeat-derived cleanup interacting with held claims → replaced by liveness (max_runtime / max_quiet_period; last_heartbeat_at columns dropped) (2026-06-17, transcript).
- Intermediate inheritor-only held-cascade filter and gate-evaluator Holds.From carve-outs → replaced by the derived held-state mechanism (option B) (2026-06-20, transcript).
- Sketch decision 4 (cascade from held fires only at commit/abandon for all downstream) → replaced by members-cascade-internally / non-members-deferred split (2026-06-20, transcript).
- HeldDurable bool skip in the cancel-siblings / parent-chain walks → replaced by the state != 'active' filter (2026-05-17, post-data-platform-cleanup, artifact).
- Delete-at-resolution → Promote-to-committed/abandoned state flip for handles that went through the full lifecycle; the two carve-out paths still Delete directly (2026-05-17, post-data-platform-cleanup, artifact).
