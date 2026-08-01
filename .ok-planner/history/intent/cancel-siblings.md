# Intent Dossier: cancel-siblings

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- `cancel_siblings: true` is a **modifier flag on the strict fan-out error policy**: after one child abandons, remaining in-flight siblings are proactively force-abandoned, saving work without changing the parent's outcome (2026-06-19, transcript).
- Cancellation is a **recursive descendant walk** reusing `ResolveClaimHandleTerminal` with a forced failure outcome, bounded by claim-tree depth, cancelling grandchildren before each row's own delete so the ON DELETE SET NULL parent FK never orphans in-flight descendants (2026-05-15/16).
- Both the sibling-cancel walk and the auto-terminal parent-chain walk skip all **non-active** claim-handle rows (`state != 'active'`): a row promoted to committed or abandoned is never a candidate for force-cancellation — the durable-Commit contract forbids undoing a successful promotion (2026-05-17).
- Terminal outcome is a closed sum: `TerminalOutcome` with OutcomeCommit, OutcomeAbandon, OutcomeAbandonSiblingCancel, OutcomeAbandonDescendantCancel plus IsAbandon()/CauseString() helpers (2026-06-19, transcript).
- The distinction is load-bearing: **sibling-cancel** (one child's abandon force-abandons in-flight siblings under strict+cancel_siblings) vs **descendant-cancel** (an abandoning claim recursively force-abandons its descendant claims top-down); the originally-failing child's own abandon is natural/direct (2026-06-19, transcript).

## Required behaviors (open promises)

- Fan-out aggregation folds N child outcomes per the declared error_policy — strict (any abandon → parent abandons), threshold(max_failures), best_effort (any commit → parent commits), first (first commit wins, remaining in-flight children cancelled); strict carries the single modifier flag `cancel_siblings: true` (2026-06-19, 08d65bfe, transcript, assistant-ratified; first promised 2026-05-15, data-platform-extensions, artifact).
- On a child's Abandon under a strict+cancel_siblings parent, remaining in-flight siblings are force-Abandoned (locked FOR UPDATE, mismatched supervisors skipped), with a recursive descendant walk cancelling grandchildren before each row's own delete — "Cancelling descendants first keeps the FK chain intact through the recursive walk." (2026-05-16, data-platform-extensions-plan-notes, artifact)
- Non-active rows (state != 'active') are skipped by both the cancel-siblings descendant walk and the auto-terminal parent-chain walk — "a row that's been promoted to `committed` or `abandoned` is not a candidate for force-cancellation" (2026-05-17, post-data-platform-cleanup, artifact).
- Descendant-cancel on claim-tree abandon: claims form a parent-child tree via `parent_claim_handle_id` (built by fan-out sub-claims and by lifetime: subgraph held claims); when a claim abandons, its descendants are recursively force-abandoned top-down (2026-06-19, 08d65bfe, transcript).
- Closed-sum terminal decision: OutcomeCommit / OutcomeAbandon / OutcomeAbandonSiblingCancel / OutcomeAbandonDescendantCancel (2026-06-19, 8a3b8c19, transcript).
- Cancelled siblings transition to failed with `error_class: sibling_failed` (2026-05-15, data-platform-extensions-plan, artifact) (artifact-only).
- `cancel_siblings: true` is the **default** for the strict policy (2026-05-15, data-platform-extensions, artifact) (artifact-only — the 2026-06-19 transcript describes the flag without confirming a default).

## Intentional absences

- **HeldDurable bool skip** in the cancellation walks — replaced by the state != 'active' predicate (2026-05-17, post-data-platform-cleanup).
- **Bot PR #10** — explicitly not merged ("Implement ourselves; do not merge PR #10"); the yaml-tag fix it addressed was done in-house (2026-06-02, rimsky-core-remediation).
- **Asymmetric Cause field** on terminal decisions (meaningful for Abandon, ignored for Commit) — replaced by the closed TerminalOutcome sum (2026-06-19, 8a3b8c19, transcript).

## Corrections and restorations (drift-fight record)

- **Silently dropped YAML keys** (2026-06-02, rimsky-core-remediation, artifact): `cancel_siblings:` and `max_failures:` on AggregationPolicy were silently dropped because the struct declared only `json:` tags and yaml.v3 falls back to lowercased field names; `yaml:` tags were added matching every sibling struct. Precedent: template surface keys must round-trip; missing yaml tags are code drift.

## Superseded / historical

- HeldDurable-based skip in the descendant walk (2026-05-16 implementation) → state != 'active' skip (2026-05-17).
- Cause-field terminal shape → TerminalOutcome closed sum (2026-06-19).
