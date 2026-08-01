# Intent Dossier: child-execution

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Child execution covers the run-side machinery by which a node's execution produces child runs (fan-out partitions) or a delegated sub-graph. **The final ruling (2026-06-23, user) is that fan-out and delegation are distinct mechanisms, not variants of one primitive**: fan-out clones the calling node N times (one clone per partition, each in its own child run-scope) and does not aggregate attribute results; delegation substitutes into the entry node and retrieves results from the exit node. Only dispatch shares a thin helper; settle is intentionally split because the fan-in shapes differ.
- There are **two settlement modes, implicit in the invocation**: carry (delegation — no policy, attribute writeback) and aggregate (fan-out — four-value author-configurable policy strict|threshold|best_effort|first). `carry_verbatim` was a runtime routing tag, never an author-facing policy.
- Settlement code is two explicitly named paths with distinct typed inputs — `SettleFromDelegate` (carry over run-scope ancestry) and `SettleFromFanoutChild` (claim-chain aggregation) — no tagged union, no per-call-site rediscrimination.
- The parent-settlement cascade bridge fires inside settlement, within the settlement transaction: no caller can settle child executions without cascading; settlement is the only run-side path that closes child execution contexts (instance termination is the administrative exception), and outcome carry-back is atomic with closing the context.
- RunScope parentage is creation-based: a sub-graph's RunScope has as its parent whatever RunScope created it — nothing to do with the frame's root; any RunScope can spawn a child RunScope (2026-06-30, user).

## Required behaviors (open promises)

- Two-mode settlement with the four-value aggregation enum owned by fan-out alone; `AggregationKindCarryVerbatim` removed (2026-06-19, 08d65bfe, transcript, user): "two *settlement modes* and *one* of those modes (fan-out) has four user-configurable *aggregation policies*… the settlement modes are implicity in invocation."
- Split settle paths with typed inputs replacing the single internally-forking SettleChildren (2026-06-19, 8a3b8c19, transcript).
- Cascade-bridge-inside-settle and settlement-as-sole-closer, transaction boundary preserved (2026-06-11, last-mile-stability, artifact): "no caller can settle without cascading; the bridge's historical absence from one path is exactly the class of defect being removed." (The unified-primitive framing around it is superseded; the invariant itself was never retracted.)
- `{{child.partition_key}}` resolves in a fan-out leaf's dispatch context to that leaf's actual partition key (2026-06-02, rimsky-core-remediation, artifact — restoration: the resolver read a field the context builder never set).
- Typed forms instead of empty-string sentinels: MessageRow.IsEmptyWake(), SettlingSignalType as *signal.TypePath (PathPtr helper), typed AggregationKind — no "empty string means default" checks (empty signal = success, empty policy = strict, empty body type = auto-match, empty graph name = main) (2026-06-19, 8a3b8c19, transcript).
- "Cross-RunScope hydration is forbidden" means **implicit** reach only: a node cannot implicitly reach nodes in other RunScopes or its own earlier runs across a scope boundary; the explicit designed channel — the calling node's attributes copied into the sub-graph's dedicated entry node and copied back out at exit — is not a violation (2026-06-22, 10cf843b, transcript, user).
- Child-dispatch accepts already-acquired sub-claims and never calls the producer's split itself; `partition_key` discriminates without schema change; the `delegate:` / `fan_out:` template surfaces are stable (2026-06-11, artifact — the surviving dispatch-side content of the unification).
- (Host-agent adjacency) Late-bound service dispatch spawns one isolated child process per run-scope, never shared across concurrent run-scopes; terminating a run-scope reaps only its own child (2026-06-08, corpus-bootstrap, artifact).

## Intentional absences

- **`carry_verbatim` as an author-facing aggregation policy** — removed; carry is delegation's policy-free mode (2026-06-19, user).
- **A single unified SettleChildren primitive / "delegation is fan-out with N=1"** — explicitly superseded (2026-06-23, user): "these are distinct things and should be documented as such." Settle is split on purpose.
- **The `carry_verbatim_requires_single_child` validation class** — mooted once carry_verbatim ceased to be declarable (it existed 2026-06-11 to guard the unified enum's degenerate case).
- **Parent-side aggregation of child attributes** — fan-out does not aggregate attribute results; fan-in is at the claim-handle level (2026-06-23, user).

## Corrections and restorations (drift-fight record)

- **partition_key never threaded** (2026-06-02): promised substitution surface existed in the resolver but not the builder — restored.
- **The unification arc as adjudication precedent**: 2026-06-06 scope-policy dropped the sketch-only "unified child-execution primitive + carry_verbatim"; 2026-06-11 built the unification anyway (resolving the duplicated-path tension — fixes had been landing in one path only); 2026-06-19 the user split settlement into two modes; 2026-06-23 the user reversed the unification framing entirely. Net: the duplicated-path disease stays cured at the **dispatch** layer only; the settle split is intent, not drift. Findings citing `concept:child-execution`'s old "exactly one dispatch path and exactly one settlement path" claim assert superseded expectations.

## Superseded / historical

- concept:child-execution's "one dispatch path, one settlement path" + five-value policy enum (2026-06-11) → two settlement modes, four-value enum (2026-06-19) → distinct-mechanisms ruling (2026-06-23).
- Deleted parallel implementations (applyTerminalCompleteSubgraphCaller + CarryExitWriteback; CreateFanOutChildren + resolveParentClaimChain) → replaced by the DispatchChildren/SettleChildren generation (2026-06-11) → settle re-split into the two named typed paths (2026-06-19).
