# Intent Dossier: claim-tree

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Claims form a parent-child tree via `parent_claim_handle_id`, built by two mechanisms: fan-out sub-claims and held claims spanning a sub-graph (2026-06-19, transcript).
- **A sub-claim is a claim** — parity where it earns its keep (2026-06-18, user): sub-claims are recursively partitionable, carry address and payload, and inherit `realized_write_semantics` and **intent** from the parent at insert time.
- Resolution walks the tree bottom-up: a parent claim auto-terminals only after all its sub-claims are terminal; the parent verdict is computed from persisted expected/committed/abandoned counters plus the snapshotted aggregation policy — never from the last-resolved child's outcome.
- Abandon cascades top-down: a claim's abandon recursively force-abandons descendant claims (descendant-cancel); under strict + cancel_siblings a child's abandon force-abandons in-flight siblings (sibling-cancel), the failing child's own abandon staying natural/direct.
- The claim tree and the RunScope tree are **parallel structures, each owned by one function**: sub-claim rows by AcquireSubClaims (claim side), fan-out partition RunScopes by CreateFanOutChildren (run-scope side).

## Required behaviors (open promises)

- Recursive partitioning via the producer's optional SplitScope: one sub-claim per returned sub-scope, child runs each holding one; sub-claims recursively partitionable; parent auto-terminals only after all sub-claims are terminal (2026-05-15, data-platform-extensions, artifact).
- Atomic acquisition (blessed invariant 10 rephrase): the acquisition transaction claims the parent run AND inserts the parent handle AND all sub-claim rows AND records Open-returned addresses — or none; producer-internal state is decoupled in the producer's own transaction (2026-05-15, artifact).
- Counter-based parent verdict: aggregation policy snapshotted onto the parent handle; strict = any abandoned aborts, threshold = abandoned>max aborts, best_effort/first = any commit promotes, unknown defaults to strict; both row-disposition branches (durable promote / delete) bump the counters and recurse the parent walk (2026-05-16, plan notes, artifact — user chose counters-on-parent over a separate audit table).
- cancel_siblings: on a child's Abandon under a strict+cancel_siblings parent, remaining in-flight siblings are force-Abandoned (locked FOR UPDATE, skipping mismatched supervisors), transitioning to failed with error_class `sibling_failed`; the recursive descendant walk cancels grandchildren before each row's own delete so the ON DELETE SET NULL parent FK never orphans in-flight descendants (2026-05-15/16, artifact).
- Cancel and parent-chain walks skip all non-active rows (`state != 'active'`): a committed or abandoned row is never a candidate for force-cancellation — the durable-Commit contract forbids undoing a promotion (2026-05-17, post-data-platform-cleanup, artifact).
- Sub-claim intent inheritance: the runtime propagates the parent's intent into each sub-claim row so the cascade sees the correct intent (2026-06-18, 8e7e4c10, transcript, user): "sub-claims should have the same intent as parent claims" — proven by asserting the persisted intent column matches the parent's.
- SubScopeDescriptor carries address and payload bytes end-to-end (wire, Go types, runtime peer bridge, insert path) so each fan-out child can read `{{claim.<producer>.payload.<key>}}`; non-empty address bytes must be JSON-valid, rejected by AcquireSubClaims with a clean precondition error (2026-06-19, 8e7e4c10, transcript).
- Each fan-out sub-claim's `node_run_id` points at its child run, so the leaf resolves its own sub-claim and the fanout_partition run-scope closure walk finds the partition scope (2026-06-02, rimsky-core-remediation, artifact).
- DataProcessing candidate timing rides the tree: BeginCandidate in the sub-claim insert transaction; CommitCandidate at leaf success; AbandonCandidate on leaf failure, sibling strict-cancel, or backfill abort (2026-05-15, artifact).

## Intentional absences

- **partition_key / producer_metadata on root claims** — deliberately fan-out-specific; parity with sub-claims only where it clarifies (2026-06-18, transcript, user).
- **Wire-carried realized_write_semantics / intent on SubScopeDescriptor** — inherited from the parent at insert time instead (2026-06-18, transcript).
- **A separate parent-verdict audit table** — user chose counters on the parent handle (2026-05-16).

## Corrections and restorations (drift-fight record)

- **Order-dependent parent verdicts** (2026-05-16): the initial recursive resolution decided the parent from the last-resolved child alone, so resolution order changed outcomes under best_effort/threshold/non-cancelling strict; fixed with snapshotted policy + counters, surfaced to and ruled by the user.
- **Durable-commit counter miss** (2026-05-16): all-durable-commit children computed Abandon under best_effort because the promote branch skipped the counter bump; fixed by separating row-disposition from resolution-outcome.
- **Plan confused the two trees** (2026-05-22, fan-out-safety divergences): plan Task 35 put fan-out RunScope creation inside AcquireSubClaims; execution refused — AcquireSubClaimsInput was deliberately NOT extended with RunScope fields. Precedent: claim-tree and run-scope-tree responsibilities do not mix.
- **node_run_id repoint** (2026-06-02): sub-claims pointing at the parent run broke both leaf self-resolution and partition-scope closure; repointed to the child run.

## Superseded / historical

- Parent verdict from last-resolved child → counters + snapshotted policy (2026-05-16).
- HeldDurable-bool skip in cancel walks → state != 'active' skip (2026-05-17).
