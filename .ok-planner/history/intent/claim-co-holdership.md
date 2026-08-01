# Intent Dossier: claim-co-holdership

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Co-holdership is declared with `holds: { <alias>: { from: <upstream-type> } }` — distinct from `claims:` (which acquires a new handle). `from:` must reference an upstream dependency, keeping the co-holdership graph acyclic. The `inherits:` directive is dead, deleted with no legacy alias.
- The acquirer plus all co-holders form the **holding subgraph**; the producer verb fires only when every holder has settled: Commit on all-success, Abandon on any-failure.
- **Held is a derived run state** (option B, 2026-06-20): a node-run is held while it participates in ANY unresolved claim handle as acquirer or co-holder — no new column or table; membership derives from claim_holders + claim_handles. On claim resolution, every holder is re-evaluated against its full claim portfolio: held→fresh if all its claims committed, held→failed if any abandoned (poison rule).
- Cascade **continues among held-subgraph members during the hold**; non-member (arms-length) subscribers see each executed holder's terminal signal only at claim resolution — terminal/success at commit, terminal/error/abandoned at abandon; holders that never executed broadcast nothing.
- Co-held claims ride the executor wire identically to acquired claims — a leaf executor cannot tell the difference — and `{{claim.<alias>.address}}`, `{{claim.<alias>.payload.<key>}}`, `{{claim.<alias>.claim_scope}}` resolve to the same bytes the original acquirer received.
- Holder rows are keyed by run identity (`holder_run_id`) and inserted at each co-holder's own acquire transaction (deferred insert).

## Required behaviors (open promises)

- `holds:` co-holds an upstream-acquired claim by alias; auto-terminal fires only when all holder rows for the handle are non-active; the holding subgraph extends to the co-holder (2026-05-15, data-platform-extensions, artifact).
- Wire-payload parity: same per-claim wire shape as `claims:`-acquired handles, including the store-handle wire entry — the executor cannot distinguish acquired from co-held (2026-05-15 plan; 2026-06-10 cascade-and-claim-handoff, artifact): "the same acquired result the original acquirer received."
- The claim's producer payload persists in a `payload` column on the claim-handle row (claimant-guarded UpdatePayload) and propagates into the co-held ClaimResult, so `{{claim.<alias>.payload.<f>}}` resolves from bytes that survive past the acquirer's acquire transaction (2026-06-10, completion report, artifact).
- Alias collision: when a node both `claims:` and `holds:` the same alias, the opened claim wins; the held entry is informational only — uniform across the substitution context and the executor wire payload (2026-06-10, artifact).
- The dispatch-time attribute-schema substitution context includes held claims, matching the lock-spec, fan-out-partition, and executor-wire paths (2026-06-10, artifact; GH issue #16 fix).
- Auto-terminal premature-firing guard: with no active holder rows, resolution consults the template's expected holding-subgraph member set and refuses to fire while an expected member has not yet inserted its row; the guard is skipped on any-failed so a failed holder drives Abandon immediately (2026-05-15, plan notes, artifact).
- A parent claim handle that is itself held defers its own Commit/Abandon until all its holders settle; the resolution path re-drives when the last holder goes non-active (2026-05-15, plan notes, artifact).
- Held-state portfolio rule: a settling co-holder with an unresolved claim transitions running→held; on claim resolution CheckAndFireResolution walks all holders; each leaves held only when its full portfolio is resolved — held→fresh if all committed, held→failed if any abandoned — and each transition fires that run's own deferred cascade to non-members; the gate evaluator's upstream-in-flight check skips held upstreams that are co-members of the receiver's held subgraph (2026-06-20, 8a3b8c19, transcript, user): "the claim resolving triggers a reevalution of all nodes holding on it."
- Held-cascade broadcast rule: every node that executed during the hold broadcasts the commit/abandon outcome to its subscribers at resolution — terminal/success at commit, terminal/error/abandoned (signal class `abandoned`) at abandon (2026-06-20, 8a3b8c19, transcript, user).
- Poison rule with forward propagation: when any holder fails, every participating holder is driven to failed via auto_terminal_abandon at the resolution moment — including in-flight holders that settle later, regardless of their own executor's verdict (2026-06-21, 10cf843b, transcript).
- Subgraph-lifetime co-held claims auto-commit at subgraph completion when the last co-holder goes non-active (2026-05-19, crimefinder, artifact).
- A held claim's lifetime is governed by the holding subgraph, not any frame: the handle stays active across frame boundaries until every holder settles; substitutions resolve to the same bytes in any frame where the subgraph is open; auto-terminal must not fire on the acquirer's settlement alone (2026-06-10, artifact).
- Held subgraphs commit-or-abandon atomically: aggregate success atomically swaps staged data into the canonical view; any failure drops the staging (2026-06-02, acceptance-coverage-recovery, artifact; the atomic-staging default).

## Intentional absences

- **The `inherits:` directive** and its entire deps-walk / ambiguity-resolution machinery (ValidateInheritance, transitive-ancestor walks) — deleted with no legacy alias; `holds:` with explicit `from:` makes the acquirer unambiguous at the source (2026-06-02, rimsky-core-remediation, reversal).
- **Eager all-members holder insert at acquirer-acquire time** — retired for the deferred per-run insert, since sibling runs don't exist yet at acquire (2026-05-15).
- **A new held state column/table** — deliberately none; held membership derives from existing claim tables (2026-06-20, option B).
- **The quality-rule subsystem** (`graph/qualityrule/`, `TemplateNodeDef.QualityRules`, `quality_rule_failed` event) — retired 2026-05-15, replaced by the verifier-executor pattern (bundled executors that co-hold claims and run checks), which is deliberately documentation plus binaries, NOT a rimsky concept or template-language sugar.
- **Blanket deferral of all cascade during a hold** — the sketch decision "cascade from held fires only at commit/abandon for all downstream subscribers" was ruled wrong by the user (2026-06-20); intra-subgraph cascade flows during the hold.

## Corrections and restorations (drift-fight record)

- **holds:-only templates never engaged the held path** (2026-06-02, rimsky-core-remediation): the held-subgraph detection layer walked only `Inherits` while insertion/resolution understood `holds:`, so documented co-held Commit/Abandon never fired. Ruled: code drift; rebuilt detection on holds.from and proved end-to-end.
- **Co-holder substitution failed at dispatch** (2026-06-10, GH #16): buildResolveContextForDispatch built the claims map from acq.Locks only and dropped acq.HeldClaims — the one path of four that dropped them. Ruled: code drift; merged held claims into the context.
- **Nil payload at co-holder acquire** (2026-06-10): without a persisted payload column the co-holder read Payload=nil and settled template_resolution_failed; fixed with the 008 migrations and claimant-guarded UpdatePayload.
- **Parent verb fired at the wrong time** (2026-05-15): a held parent's Commit/Abandon fired before its own holders settled; fixed with the holder-check inside the parent's locked read.
- **Held-cascade sketch corrected by user** (2026-06-20, 8a3b8c19): the sketch deferred all cascade until commit/abandon; the user pointed out commit/abandon happens after group execution, so holders must keep cascading within the claim scope during the hold — the intra-subgraph-flow + deferred-external-broadcast model replaced it.

## Superseded / historical

- `inherits:` with deps-reachability acquirer inference → `holds: {from:}` (2026-06-02).
- Holder rows keyed by node id, eagerly inserted for all members → keyed by run id, deferred to each holder's acquire tx (2026-05-15).
- Sketch decision "defer all held cascade until commit/abandon" → intra-member cascade during hold, per-holder deferred broadcast at resolution (2026-06-20); intermediate inheritor-only cascade filters and gate-evaluator Holds.From carveouts superseded by the derived held-state mechanism.
