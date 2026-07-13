# Intent Dossier: error-policy

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- The operator surface is per-node `error_types:` in the template — deliberately a map name, not `error_policy:` (which would falsely imply one policy); the implementation entry point is `applyErrorPolicy` (2026-05-12, nomenclature-resolution, artifact).
- The action vocabulary is the closed 4-value set **retry, give_up, pass, release_and_requeue**. Each error class maps to exactly one action — no per-class action chains, no per-class retry counts (2026-06-22/23, 10cf843b, transcript).
- A single node-level **MaxRetries** (plus node-level **RetryBackoff**) bounds total retries across all error classes; the counter advances on any retry regardless of class and resets only via a new node-run (2026-06-22, 10cf843b, transcript).
- **Retry is in-place**: a loop on the same node_run row — the claim (a lock) stays held, the executor is re-invoked on the same run with the same claims and bag; only give_up transitions the run to failed. Claim-acquisition errors retry in-place through the exact same machinery as executor errors — one unified path (2026-06-22, 10cf843b, transcript).
- Undeclared error classes default to **give_up** — including `acquire/unavailable`, which gets no special-case default; operators wanting the old silent-requeue behavior must declare `release_and_requeue` (2026-06-22, 10cf843b, transcript; give_up-on-undeclared-acquire first decided 2026-05-23, signal-taxonomy, artifact).
- Retry loops are deterministic from the operator's point of view: no hidden supervisor-side cap may override an operator's declared retry count (2026-06-22, 10cf843b, transcript).
- **Infrastructure-class errors intentionally skip operator-declared error policy** (terminal-resolution owns them); they get a supervisor-side default retry cap of 10, with an operator-declared node-level MaxRetries winning whenever set — the one place a supervisor default applies (2026-06-22, 10cf843b, transcript; adjudicated fix-doc 2026-07-13, 3f71f90a, transcript, finding 54).
- Nodes carry **no runtime state**: the evaluator state (retry counter) is node_run-local, persisted on rimsky_node_runs so cap accounting survives supervisor crashes; rimsky_nodes carries only template-derived identity and scheduling metadata (2026-06-22, 10cf843b, transcript).
- Error classes are free-form hierarchical slash-leaves under the `terminal/error/<class>` taxonomy: per-executor vocabularies (`http/*`, `agent/*`, `pg/*`, `verifier/*`), the `acquire/*` synthetic family for pre-dispatch acquisition failures, and consumer-invented domain classes are all legal (2026-05-23, signal-taxonomy, artifact; reaffirmed 2026-06-15, 8c66c02c, transcript).
- Executors advertise `declared_error_classes` (trailing-`/*` wildcards allowed) via observability capabilities; claim-producers may declare a vocabulary too. The template validator range-checks `error_types:` keys against the union of executor-declared classes, the `acquire/*` family, runtime-synthesized classes, and producer-declared classes; a key attributable to no declared vocabulary registers with an **advisory warning, never a hard rejection** (2026-06-11, last-mile-stability, artifact — this is the latest validation stance, softening earlier hard-rejection at registration from 2026-06-02, acceptance-coverage-recovery, artifact).
- Resolution decouples three axes — signal, dispatch disposition, settled color (fresh | failed | parked); color is informational only and cascade never gates on it. `pass` settles the run fresh but still emits `terminal/error/<class>` so subscribers can see the error; retry emits `transient/retry/<attempt>/<class>` (2026-05-23, signal-taxonomy, artifact).
- A per-sender `terminal/error/<class>` subscription fires whether the sender settles via give_up (failed color) or pass (fresh color) — the signal-blind property (2026-06-10, cascade-and-claim-handoff, artifact).
- `POST /nodes/{id}/reset` survives as a **pure retry-budget clear** on failed-terminal rows only (409 otherwise): no wake, no invalidation; the operator posts a message afterward to re-run. Parked and pending-claim are legitimate waiting states with no operator-intervention carve-out (2026-06-16/17, 4c42fe5b + b95ff4a7, transcript).
- Fan-out aggregation error_policy is the 4-value family strict (default; cancel_siblings modifier) | threshold(max_failures) | best_effort | first, emitting `terminal/error/aggregate/{strict,threshold,first}_failed` leaves (2026-05-15, data-platform-extensions, artifact; reconfirmed 2026-06-19, 08d65bfe, transcript).
- Opaque executor scratch bytes carry forward byte-for-byte across error-policy retry (2026-06-17, b31002b8, transcript).
- Producer errors cross the HTTP boundary intact: error class + message in the body, 422 producer-rejected vs 502 producer-failed vs 500 rimsky-internal (2026-06-11, last-mile-stability, artifact).
- Retry-budget enforcement lives in the node-level policy chain, never in stores/producers (2026-05-06, fs-store-pick-policy, artifact).

## Required behaviors (open promises)

- One action per declared error class from {retry, give_up, pass, release_and_requeue}; runtime honors each at the appropriate error site; undeclared → give_up (2026-06-22, 10cf843b, transcript; "or change the design so it's only 'max retries' and operators don't set per-error class retry limits").
- In-place retry on the same node_run for both executor and acquisition errors, claim held throughout; a held-participating run that exhausts retries routes through give_up, whose lock release with success=false poisons the claim and auto-terminal abandons all holders (2026-06-22, 10cf843b, transcript).
- `release_and_requeue` releases the dispatch row and any locks so the scheduler re-picks the node later (2026-06-22, 10cf843b, transcript).
- Node-level MaxRetries + RetryBackoff enforced across all classes; counter persisted on the node_run row (2026-06-22, 10cf843b, transcript: "persisted. it was persistent on the node table; we are just moving it to node_run, right?").
- Both the errored original run and the retry-completed run carry a settling signal — the NULL settling_signal_type on the errored original was flagged as a bug being fixed in a parallel session (2026-06-21, 21306ffe, transcript).
- Declared error classes must have real emit sites that reach subscribers via error_types routing — never config-validating-but-never-sent (2026-06-06, comprehensive-gap-closure, artifact: "these declared classes carry real signals").
- Bundled-executor classes are hierarchical prefix leaves (`pg/verifier_check_failed/<check_kind>`, `http/server_error/*`, `agent/tool_use_failed/*`) so subscribers can prefix-match a family or pin a leaf (2026-06-15, 8c66c02c, transcript — flagged as awaiting promotion to a durable decision, slug error-class-hierarchical-leaves).
- Per-ParkReason maximum park duration: per-row max_park_duration_seconds → deployment per-reason cap → no cap; timeout resolves failed with error_class `park_timeout` (2026-06-15, 8c66c02c, transcript — awaiting promotion, proposed slug parked-per-reason-max-duration) (artifact-decided 2026-05-15, cited in code).
- `POST /nodes/{id}/reset` clears evaluator state, settling-signal-type, and stale frame_id on a failed-terminal row; refuses 409 otherwise; no wake (2026-06-16/17, 4c42fe5b + b95ff4a7, transcript; "signed off").
- Executor scratch observed identical on the successor invocation after an error-outcome retry (2026-06-17, b31002b8, transcript).
- pg/swap_failed surfaces as a classed error at the gRPC terminal-verb boundary (google.rpc.ErrorInfo, domain rimsky.store-postgres), decoded into the holder's error_types routing — never a tx-fatal executor Error frame (2026-06-06, comprehensive-gap-closure, artifact) (artifact-only).
- Unavailable carries error_class on the claim-producer wire (Unavailable.error_class → OpenOutcome.UnavailableClass) so a declared class on the Unavailable arm reaches the operator's chain (2026-06-06, comprehensive-gap-closure, artifact) (artifact-only).
- claude-agent emits its declared classes for real (agent/context_exceeded, agent/refused, agent/tool_use_failed/<tool> unconditionally; agent/rate_limited only when handle_rate_limits=false — the default path parks instead); agent/signoff_unobtained is declared and routes through error_types (2026-06-04 + 2026-06-06, artifacts) (artifact-only).
- http-node: 429+Retry-After parks with resume_at; 4xx with the configurable error-class JSON field maps to http/request_invalid/<value>, absent field yields a stable `/_unspecified` leaf (2026-06-06 + 2026-06-08, artifacts) (artifact-only).
- Substitution-failure handling lives where substitution runs: the gate evaluator catches classifiable substitution errors inline in the sender's terminal-apply transaction and drives the receiver to its template_resolution_failed policy; non-classifiable DB faults still propagate (2026-06-21, 10cf843b, transcript).
- Producer error classes cross the HTTP boundary with 422/502/500 discrimination (2026-06-11, last-mile-stability, artifact) (artifact-only).
- Host-agent/proxy classes (host_agent_not_connected, binding_not_found, spawn_failed, host_agent_disconnected, contract_mismatch, executor_crashed) ride the existing error_types chain — no new policy mechanism (2026-05-24, host-agent-and-proxy, artifact) (artifact-only).

## Intentional absences

- **Per-class action chains, per-class retry counts, the three-field evaluator cursor (action_index / retry_counter / current_error_class), the no-progress counter, and `max_retries_without_progress`** — deliberately simplified away in commit 339809cc: alternating error classes could loop indefinitely (cursor reset per class change) and the model "didn't seem useful in any practical scenario" (2026-06-22/23, 10cf843b, transcript). Tests asserting the old model were removed rather than the feature restored (2026-06-29, 8a8539a4, transcript).
- **The hidden supervisor safety-net cap** (shouldForceRetryLoopGiveUp, silent default 100) overriding operator-declared retry counts — removed as wrong design (2026-06-22, 10cf843b, transcript).
- **Fresh-row retry** (each retry a new node_run via CreateNonCascadeStale), the `policy_retry` / `infra_reenqueue` creation reasons, the `retry_after_error` prior-dispatch disposition, prior_dispatch back-pointer fields, and the applyHeldAbort/held-probe special case — all retired by in-place retry (2026-06-22 + 2026-06-30, 10cf843b + 8a8539a4, transcript).
- **The `invalidate` policy action and all send-side invalidate.targets emits** — retired 2026-05-14 (subscription-cascade, artifact); reactive coupling is impactee-side `subscribes:` only.
- **discard_then_retry / resume_then_retry / discard_claims_then_retry** — resume_then_retry retired entirely (behavioral alias), discard_then_retry renamed discard_claims_then_retry (2026-05-23, signal-taxonomy, artifact); the final closed set then replaced discard_claims_then_retry with release_and_requeue — per the doc correction, "the discard-claims action named in the concept doc never existed; the behavior it described is release_and_requeue" (2026-06-23, 10cf843b, transcript).
- **The lifecycle-handler concept** (on_executor_complete / on_executor_errored / on_acquire_unavailable slots, and earlier on_executor_blocked; the by_changed | always_propagate | never_propagate resolves) — retired entirely 2026-05-23 (signal-taxonomy, artifact); cascade-fire is subscriber-driven, a sender cannot suppress downstream firing.
- **The Blocked terminal variant and the cross-executor synthetic classes executor_blocked / executor_internal_error** — Blocked collapsed into Error (2026-05-12, nomenclature-resolution, artifact); the shared synthetics retired with each executor using its own leaf (agent/blocked, http/internal_error) (2026-05-23, signal-taxonomy, artifact).
- **A pass_record action** (silently treat error as success) — declined: would hide the error from subscribers (2026-05-23, signal-taxonomy, artifact).
- **transient/on_error_resolved and transient/policy_resolved signals** — declined as vestigial/duplicative (2026-05-23, signal-taxonomy, artifact).
- **Silent implicit retry on acquisition failure** — reversed to fail-fast give_up default (2026-05-23, signal-taxonomy, artifact; reaffirmed with release_and_requeue as the explicit opt-in, 2026-06-22, 10cf843b, transcript).
- **Predicate language for handler conditions, generalized frame-end predicate hooks, hard frame timeouts, per-claim on_unavailable overrides** — deferred/out of scope 2026-05-05 (reactive-loops, artifact); largely mooted by the lifecycle-handler retirement.
- **A rimsky-native verifier-node alternative for agent output attestation** — rejected; validation in a second dispatch gives no in-session correction (2026-06-04, claude-agent-signoff-gate, artifact).
- **carry_verbatim as an author-facing aggregation policy** — removed from the enum; it was a runtime routing tag, the real family is the 4-value strict|threshold|best_effort|first (2026-06-19, 08d65bfe, transcript).

## Corrections and restorations (drift-fight record)

- **error-policy.md claimed infra errors route through operator policy** — drift from a June doc-sweep; the terminal-resolution concept (infra skips operator policy) is authoritative; adjudicated fix-doc, finding 54 (2026-07-13, 3f71f90a, transcript).
- **Phantom discard-claims action in the concept doc** — never existed in code; docs corrected so operators cannot write YAML naming it (2026-06-23, 10cf843b, transcript).
- **retry_after_error still described as a valid prior disposition** in several docs after in-place retry — corrected (2026-06-30, 8a8539a4, transcript).
- **Retry-cap sentence in the concept doc**: the deployment-level cap is a supervisor default, not a scheduler default — corrected (2026-06-11, last-mile-stability, artifact).
- **Per-sender terminal/error subscription silently skipped on v0.6.0** (GitHub issue #15) — restored: fires on both give_up and pass settlement (2026-06-10, cascade-and-claim-handoff, artifact).
- **acquire/unavailable wrote a legacy kind="error" audit row instead of a canonical signal** — fixed to emit terminal/error/acquire/unavailable (or transient/retry/<n>/acquire/unavailable) from the state-transition tx (2026-05-24, signal-taxonomy divergences, artifact).
- **claude-agent's four declared classes had zero emit sites** — emit sites added (2026-06-06, comprehensive-gap-closure, artifact).
- **YAML keys cancel_siblings:/max_failures: silently dropped** (json-only struct tags); fixed in-house, bot PR #10 explicitly not merged (2026-06-02, rimsky-core-remediation, artifact).
- **Retry-no-progress counter reset to zero on every retry** (row delete + re-enqueue), so the cap never tripped — fixed in-tx (2026-05-08, platform-extensions, artifact; the whole cap later retired).
- **give_up from stale was an illegal transition silently swallowed** — NextState extended (2026-05-05, reactive-loops, artifact).
- **Fan-out claim-tree parent verdict depended on child resolution order** — fixed with counters-on-parent (user chose over an audit table) (2026-05-16, data-platform-extensions, artifact).
- **Empty-string sentinels swept**: AggregationPolicy.Kind became a typed enum with strict stamped at load/insert; SettlingSignalType became nullable *TypePath with honest nil-checks (2026-06-19, 08d65bfe, transcript: "yes, fix them all").

## Superseded / historical

- Three-action vocabulary (retry, invalidate(targets), give_up) → corrected to five (2026-05-04) → four (retry, invalidate, give_up, pass; 2026-05-12) → invalidate retired (2026-05-14) → pass/give_up/retry/discard_claims_then_retry tuple model (2026-05-23) → the final closed set retry/give_up/pass/release_and_requeue (2026-06-22, transcript).
- Four lifecycle-handler blocks (2026-05-04/05) → three slots (2026-05-12) → resolve+error_class only (2026-05-14) → concept retired entirely (2026-05-23).
- max_retries_without_progress cap with default 100 and per-node override (2026-05-08, 2026-05-23) → replaced by node-level MaxRetries; hidden 100 default removed (2026-06-22, transcript).
- Cross-dispatch retry rows linked via prior_dispatch_id → in-place retry on one row (2026-06-22, transcript).
- Hard rejection of unknown error_types keys at registration (2026-06-02, 2026-06-04) → advisory warning for keys attributable to no declared vocabulary (2026-06-11).
- "Error-class strings are not globally registered, any string accepted" (2026-05-19) → declared-vocabulary range-checking with warnings (2026-05-23 → 2026-06-11).
- Non-cascade re-run paths go direct-to-stale with a fresh row for policy_retry (2026-06-20, transcript) → policy_retry retired by in-place retry two days later (2026-06-22, transcript).
- YAML PolicyAction retaining Targets/Frame parse-compat fields through the retirement window (2026-05-23) — a transitional affordance, ignored by the runtime.

## Conflicts needing human ruling

None recorded — the apparent tension between "infra errors skip operator policy" and "operator MaxRetries wins for infra retry caps" resolves cleanly: infra classes bypass the error_types class map, while the numeric retry budget still respects an operator-declared MaxRetries over the supervisor default of 10 (both 2026-06-22, 10cf843b, transcript; 2026-07-13 adjudication).
