# Intent Dossier: cascade-mode

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- There are four cascade modes: most-recent (the default), sequenced, idempotent-queue, idempotent-settled. All four are implemented in full, per spec, with no template-validation gate holding the opt-ins back (2026-06-20, user).
- The mode is a per-template-node field `cascade_mode`; one mode per node, not per cascade source. Empty defaults server-side to most-recent; unknown values are rejected at template-parse time with a field-level error naming the four valid options.
- Cascade modes govern the intra-frame node_run queue only. They have nothing to do with messages: messages cannot queue on a node or node_run (one message = one frame); the per-instance message-queue coalesce is a different mechanism. This distinction has been repeatedly confused and must stay explicitly disambiguated.
- Modes apply at the pending-to-stale transition — the moment the attribute bag is resolved — never at node-run creation. Wait-set accumulation is a separate rule from queue/mode semantics.
- At pending→stale the bag is built first (next-most-recent stale or predecessor run, plus drained wait-set results), then the mode applies: most-recent overwrites an existing stale; sequenced queues a new stale row; idempotent compares bags.
- "Most recent" means most-recent-while-pending: a run already transitioned to stale with its bag built from snapshot is committed (though most-recent may overwrite a prior stale at the transition itself).
- Idempotent-mode bag comparison uses RFC 8785 JCS canonicalization (the existing lib/graph/template/canonical jcs helper), never raw json.Marshal, and compares the resolved (post-substitution) input bag against the prior run's stored resolved bag.
- Non-cascade creation paths (operator_invalidate, recalculate) are created directly as stale, immune to mode rules, never walker accumulation targets, and ordered by a monotonic sequence column so they do not jump the queue.
- Sequenced preserves per-round attribute snapshots: each queued dispatch sees the sender value of the run that drove its round (pinned wait-set sender_run_id), not the newest settled sender (restored 2026-07-13, commit 2d4952e4).

## Required behaviors (open promises)

- Four modes configured via cascade_mode, implemented in full, no gate (2026-06-20, 8a3b8c19, transcript, user): "we want these paths implemented in full, per the spec, no gate."
- most-recent is the default; the sequenced/idempotent modes are opt-ins (2026-06-19, 8a3b8c19, transcript, user): "we'd do most-recent as the default … the naive solution is probably best."
- Mode rules apply at pending→stale, after the bag is built; the bag is composed from the next-most-recent stale (or predecessor run) plus drained wait-set results (2026-06-20, 8a3b8c19, transcript, user).
- Idempotency applies only when one or more stale rows sit in the queue (idempotent-queue), comparing against the most recent stale's bag; with no stale, the run is always created — a fresh-settled predecessor never suppresses a dispatch under the queue variant (2026-06-20, 8a3b8c19, transcript, user). idempotent-settled additionally compares against the most recent settled run (2026-06-20, transcript).
- Idempotent dedup must actually drop identical-input cascade rounds — the resolved post-substitution bag against the prior run's stored resolved bag — with substitution-failure routing staying before the mode rule so failures hit error policy rather than being silently dropped (2026-06-22/23, 10cf843b, transcript).
- JCS (RFC 8785) canonicalization for all idempotent comparisons, via the existing canonical jcs helper (2026-06-20, 8a3b8c19, transcript).
- dispatch_input_bag is preserved in its own column, never overwritten by executor writeback: carry-forward reads the live bag, idempotency reads the preserved input bag; wait-set keying per receiver run unchanged; non-cascade rows carry no wait-set rows (2026-06-20, 8a3b8c19, transcript).
- Non-cascade rows (operator invalidate, recalculate) are created directly as stale with carry-forward bag at creation, immune to modes, carrying sequence and creation_reason columns; the serialization gate (no other run for the node/run-scope in running/held/parked) lives at the dispatcher claim site (2026-06-20, 8a3b8c19, transcript). The creation_reason set is cascade, operator_invalidate, recalculate (narrowed 2026-06-22, 10cf843b, transcript).
- Template validator rejects unknown cascade_mode values at parse time, field-level error naming the value and the four valid options; empty accepted, defaulting to most-recent (2026-06-22, 10cf843b, transcript).
- Story + proof coverage: cascade-defers-during-flight, held-commit-cascades-success, held-abandon-cascades-abandoned, operator-invalidate-queues-during-flight, most-recent-coalesces-cascades, sequenced-preserves-cascade-rounds, idempotent-mode-dedupes each have a passing postgres-backed scenario proof; modes are user-facing and have stories plus a dedicated cascade-mode concept page (2026-06-20 + 2026-06-21, transcript). Proofs drive cascade rounds inside a single frame via template loops / self-subscription (a cascade self-edge bounded by a CEL when: predicate), never via posted messages (2026-07-03 + 2026-07-05, 3f71f90a, transcript).
- Cascade walker behavior: accumulates pending rows per sender node, dedupes natural-cascade inserts by sender_run_id; the drained-only wait-set filter is removed (2026-06-21, 10cf843b, transcript).
- Four gate fixes stand as ratified root fixes: receivers blocked by an upstream in-flight run are re-evaluated when the blockers settle (reevalDownstreamReceiverGates); the cascade-mode drop check runs before the same-node serialization check; most-recent drops a pending run whenever a later cascade peer exists regardless of iteration order; idempotent comparison resolves a still-pending prior peer's bag on demand (2026-07-05, 3f71f90a, transcript).
- Sequenced per-round value preservation: when A settles v1, v2, v3 while B is held, B's three queued dispatches see v1, v2, v3 respectively. populateSubscribedSenderDeps resolves each round-driving sender from its pinned wait-set sender_run_id, falling back to GetMostRecentSettledRun only for subscribed senders that did not drive the round; the sequenced proof asserts per-round values (2026-07-13, 3f71f90a, transcript; committed 2d4952e4).

## Intentional absences

- No queue cap for sequenced/idempotent modes: both the node-fail cap and drop-on-overflow were declined; unbounded queue under stuck in-flight plus sustained cascades is an accepted user-facing risk (2026-06-20, 8a3b8c19, transcript, user).
- No operator-invalidate force mode (killing the in-flight predecessor): operator invalidation waits behind the in-flight run (2026-06-20, transcript).
- No per-cascade-source mode rules: a node has exactly one cascade mode (2026-06-20, transcript).
- No deferred worker for the drain handler / gate evaluator: stays synchronous in-transaction (2026-06-20, transcript).
- No walker-time reach-back: most-recent must not drop runs already transitioned to stale at cascade-walker time (2026-06-20, transcript, superseding an assistant proposal).
- Only serial_queue frame_resolution_mode is supported; an interleaved-frames mode would need new carrier machinery (2026-05-14, subscription-cascade-and-quality-of-life, artifact) `(artifact-only)`.
- Cross-frame convergence behaviors: excised — cascade is intra-frame; the north star is frame isolation (2026-07-05/06, 3f71f90a, transcript).

## Corrections and restorations (drift-fight record)

- Message-queue conflation: the story most-recent-coalesces-cascades as written conflated the per-instance message queue with the node_run queue; node coalesce modes have nothing to do with messages (2026-07-03, user). The stories sequenced-preserves-cascade-rounds and idempotent-mode-dedupes stand as written (cascade layer); only their proofs were broken for using posted messages, and were rewritten to template loops (2026-07-03, user).
- Retirement of the five foundation cascade proofs (attribute-changed diff-gate, sequenced, idempotent, defers-during-flight, most-recent) was rejected outright: restore them, diagnose the walker, make every proof green — "no deferrals, no follow-ups … we won't accept any unknowns" in foundation (2026-07-05, user).
- Backward-pointing design docs (story:queue-drain-converges cross-frame promise; concept:signal's two-step prior lookup) caused an agent to course-correct code back to retired cross-frame behaviors (diff-gate widening, commit 20724e96). Ruling: the docs were the drift; excise the backward-pointing surfaces (2026-07-06, user).
- Sequenced cardinality-only regression: commit bc3280d7 (Jun-22) incidentally switched sender reads from pinned sender_run_id to GetMostRecentSettledRun while fixing an unrelated substitution-set bug; July-5 frame-isolation work removed the masking timing; the July-5 test commit (47cea918) rationalized the regression as a "story-level intent shift". Ruled fix-code (finding 2337 flipped from fix-doc lean); per-round restoration implemented immediately, keeping bc3280d7's genuine fix (template-declared subscription sender set) (2026-07-13, transcript).
- A known idempotent-queue dedup gap (one extra cascade slipping past, 4 dispatches vs 3) was acknowledged and deferred as an explicit task (2026-06-22), then the dedup rule was firmed to resolved-bag comparison (2026-06-23).

## Superseded / historical

- Assistant recommendation of sequenced-with-idempotency as default and not shipping most-recent → user chose most-recent default with sequenced modes opt-in (2026-06-19).
- Three-mode set (sequenced, sequenced-with-idempotency, most-recent) → four modes with the idempotency split into idempotent-queue / idempotent-settled (2026-06-20).
- Delete stale-not-claimed runs at walker time → most-recent-while-pending rule (2026-06-20).
- Gate-evaluator-with-three-trigger-sites proposal for non-cascade paths → direct-to-stale creation with sequence + creation_reason (2026-06-20).
- policy_retry / infra_reenqueue creation reasons and prior_dispatch back-pointers → dropped with in-place retry (2026-06-22).
- Single mode enum on subscription entries → two orthogonal booleans wake_on_change / force_upstream_refresh on subscription edges (2026-06-14, 37e2ea5e, transcript) — a subscription-edge concern adjacent to, not part of, the node cascade_mode field.
- July-5 claim that sequenced guarantees only queue cardinality → reversed 2026-07-13 (per-round bag content restored).
