# Intent Dossier: _retired

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

This is the registry concept for deliberately killed mechanisms. The controlling ruling is
transcript-tier (2026-07-11, 3f71f90a, assistant-ratified): a fixed set of retired mechanisms
**must not appear as current anywhere in code or design docs**. Docs still naming them as
current (e.g. wake-on-change as a required flag with a rejection invariant) are drift and
would mislead a maintainer into re-adding deleted behavior. Adjudicators: any finding whose
expectation depends on an item below is refuted; any doc presenting one as current is fix-doc.

## Intentional absences

The 2026-07-11 retired-mechanisms list (3f71f90a, transcript) — each must not appear as current:

- `rimsky_frames.state`
- `terminate_after_run`
- `emits_message` (replaced by the message-sender-node utility-node design and the send-vs-emit vocabulary; see message-sender-node dossier)
- named-event (also confirmed absent as a lineage record kind, 2026-06-17; see lineage-record dossier)
- per-instance main RunScope
- `wake_on_change`
- `subscription_scope` column (migration 016)
- `attribute_overrides_match_counts` column (migration 020)
- `userdata_schema`
- `fresh_changed`
- declared-events
- `last_heartbeat_at`

Earlier artifact-tier retirements, consistent with and predating the list:

- **quality-rule in-process evaluator tree** (`graph/qualityrule`) — stays deleted per the 2026-05-15 clean-break deprecation; licensing constraint attached: no AGPL leakage into the Apache-licensed `stores/postgres` and shared SQL-checks packages (2026-05-19, multi-instance-template-ergonomics, artifact).
- **lifecycle-handler concept** — retired entirely: the three template slots (`on_executor_complete`, `on_executor_errored`, `on_acquire_unavailable`) collapsed into the unified error-policy surface plus direct fixed signal emission; the `by_changed | always_propagate | never_propagate` resolves retired because cascade-fire is subscriber-driven and "the sender cannot suppress downstream firing"; the TemplateNodeDef fields, handler types, and validators all deleted (2026-05-23, signal-taxonomy-and-policy-decoupling, reversal).
- **last-outcome concept and `last_outcome` column** — retired entirely (schema migration drops the column); granularity moved into signal payload fields (`changed` on terminal/success, `discarded_claims` on transient/retry) and the lineage `settling_signal_type` field (2026-05-23, signal-taxonomy-and-policy-decoupling, reversal).

## Corrections and restorations (drift-fight record)

- The 2026-07-11 ruling itself is a drift-fight instrument: it was recorded precisely because docs were found still naming retired mechanisms as current (wake-on-change cited as the example). The precedent it sets: when a retired item appears as current, the doc is stale — fix doc, never restore the mechanism.
