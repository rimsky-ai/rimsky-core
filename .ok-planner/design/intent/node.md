# Intent Dossier: node

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- **Nodes carry no runtime state.** No lifecycle phase, no policy-evaluation cursor, no retry counter, no frame engagement: rimsky_nodes holds only template-derived identity and scheduling metadata; EvaluatorState (retry_counter, action_index, current_error_class) is node_run-local; the mutable frame_id column on the node row is removed; node identity rows are immutable during frame processing (2026-06-21..2026-06-22 and 2026-07-05, 10cf843b/3f71f90a, transcript).
- Only a node_run has state; "fresh" is a terminal run state — a never-dispatched node is in no state and fresh must never be synthesized for a node with no runs (2026-06-21, 10cf843b, transcript).
- The honest operator-facing node surface is a 4-bucket categorical run summary — active = running+held+parked, pending = pending+stale, fresh, failed — plus latest attributes; deciding how to display runs is the dashboard's problem (2026-06-20..2026-06-21, transcript).
- Cascade involves only node_runs: state transitions target a specific node_run, which knows its own state, scope, and frame; there is no legitimate lookup of "cascade state" by (node_id, run_scope_id) (2026-07-05, 3f71f90a, transcript).
- Messages ARE nodes at runtime: message types materialize as literal pass-through no-op node rows at instance creation; delivery validates the body, creates a stale node-run with creation_reason message_delivery, copies the payload verbatim into the run's attribute bag, and lets the ordinary scheduler/cascade settle it. `messages` is a registration-type-checked lexical alias for `nodes` over one substitution pipeline; message-receiver nodes differ from ordinary nodes only in that they cannot subscribe or substitute in (2026-06-19 and 2026-06-22, 8a3b8c19/10cf843b, transcript).
- Message emission is a dedicated node-kind (rimsky.emit_message utility node via the in-process executor), not a capability of arbitrary nodes (2026-06-19, 8a3b8c19, transcript).
- Utility nodes (kind:) are fully encapsulated in-process executors using only the ordinary executor protocol — no runner-side special casing; loop_counter is the bundled exemplar; declaring both kind and executor is a registration error (2026-06-14 and 2026-06-16, 752fe200/055468fc, transcript).
- Operators must not invoke nodes in arbitrary order: direct node targeting exists only from the debug channel (breakpoint, permission-gated) plus one named carve-out — POST /nodes/{id}/reset as a pure retry-budget clear on failed-terminal rows (no wake, no invalidation, 409 otherwise) (2026-06-15..2026-06-17, 4c42fe5b/91ec93d1/b95ff4a7, transcript).
- Rimsky-as-circuit-graph is the design direction: anything beyond managing a node cascade is modeled with nodes and propagation via generic composable primitives; no use-case-specific features (2026-06-14, 752fe200, transcript).
- If a node has attributes, they must have a schema; there is no attributes-without-schema shape (2026-06-15, c60b550a, transcript).
- Per-node service configuration (e.g. claude-agent cli.mcp_servers with http/stdio/module transports, cli.expose_env) lives in node config owned by the template author, rides the existing dispatch payload with no rimsky protocol change, and intersects with operator-side allowlists enforced by the service (2026-07-01..2026-07-03, 8a8539a4, transcript).
- Lifecycle handlers: three declarable slots (on_acquire_unavailable, on_executor_complete, on_executor_errored) plus the key-indexed on_event map; error variants discriminate via error_class under the single error handler (2026-05-12, nomenclature-resolution, artifact).

## Required behaviors (open promises)

- Illegal node-run transitions error rather than silently no-op — a double dispatch-claim must surface (2026-05-04, foundation-contract, artifact): "The foundation does not silently absorb double-claims."
- EvaluatorState and retry budget live on rimsky_node_runs; rimsky_nodes carries only template-derived identity and scheduling metadata (2026-06-22, 10cf843b, transcript): "nodes have no runtime state."
- The node read API exposes run_summary (exact mapping: active = running+held+parked, pending = pending+stale, fresh, failed) and the fields the node row owns plus latest attributes — never a synthesized single-value state (2026-06-21, 10cf843b, transcript).
- Forensic last-attribute: the control-api/observability node surface returns the node's most recent resolved attribute bag — the values actually dispatched — from real persistence (2026-06-06 and 2026-06-08, corpus-bootstrap, artifact; consistent with the 2026-06-21 "latest attributes" surface).
- Message-receiver nodes: literal rows created at instance creation, one per declared message type, empty executor, payload copied verbatim (no receiver-side substitution), settled via the existing pure_cascade empty-executor path firing standard terminal/success cascade (2026-06-19 and 2026-06-22, transcript): "the message node *is* exactly the message, and its attributes are exactly the message payload."
- One substitution pipeline: messages-prefix names type-checked at registration as message-virtual nodes, nodes-prefix as non-message nodes; no runtime branching, no parallel parse/extract/resolve paths (2026-06-19, 8a3b8c19, transcript).
- rimsky.emit_message as a utility node: builds the envelope from resolved attributes, inserts into the message ledger, returns terminal/success; canonicalized from sugar at template registration; no special-case branches in dispatch/terminal/scheduler trunks (2026-06-19, 8a3b8c19, transcript).
- loop_counter utility node: required max input attribute, executor-written count carrying forward across dispatches in a RunScope, emits loop while count < max and done at max (2026-06-14, 752fe200, transcript).
- Utility kinds dispatch with no external executor service and no extra deployment; template registration rejects unknown kinds like unknown executors; the sugar field is `kind:` (not `type:`) (2026-06-14, 752fe200, transcript).
- Utility nodes have no runner-side special accommodation — a timer node, if built, is exactly like the loop node (2026-06-16, 055468fc, transcript).
- POST /nodes/{id}/reset is a pure retry-budget clear: clears the failed-terminal row's evaluator state, settling-signal-type, and stale frame_id; synthesizes no wake envelope, opens no frame, refuses 409 on non-failed-terminal (2026-06-16 and 2026-06-17, 4c42fe5b/b95ff4a7, transcript; user "signed off").
- Fan-out children reuse the parent's node row, so the parent's per-node userdata reaches every child verbatim in ExecuteRequest (2026-05-19, crimefinder, artifact).
- Nodes carry free-form template-declared tags: pure operator metadata, {{params.*}}-only substitution at instance materialization, typed failure on missing param (2026-05-19, multi-instance-template-ergonomics, artifact).
- Nodes with attributes always have an effective schema (executor nodes get it from the expected_attributes_schema merge) (2026-06-15, c60b550a, transcript).
- Claude-agent per-node declarations: cli.mcp_servers inline (all three transports — http, stdio, module, with http-loopback aliasing module) and cli.expose_env; the service enforces the intersection with operator allowlists and rejects off-allowlist dispatches with errors naming the entry, template, and node; spawned CLI children see only their own node's declared env vars; plaintext env values never enter the persisted attribute bag (2026-07-01..2026-07-03, 8a8539a4, transcript).
- Per-node service config rides the existing dispatch payload — no new rimsky protocol or payload fields; the redesign lives in the bundled services (2026-07-01, 8a8539a4, transcript).
- Message-queue coalesce mode cancels ALL pending messages per instance regardless of type; the template validator warns when a coalesce-mode node declares two or more distinct message types (2026-07-06, 3f71f90a, transcript).
- Lifecycle-handler mechanics: each slot declares resolve + optional invalidate; a declared invalidate fires unconditionally whenever the handler runs, orthogonal to resolve; reserved target `self` resolves to the declaring node's type; absent handlers preserve default behavior (2026-05-05, reactive-loops, artifact).
- on_event: key-indexed {event_name → handler} map dispatching per executor-emitted named event, sharing the resolve + invalidate vocabulary (2026-05-11, log-convergence, artifact). Named events are triggers, attributes are state — a loop node's result is its event, its count an attribute (2026-06-14, 752fe200, transcript).
- Multi-loop orchestrator patterns must be expressible through generic composable primitives only — no coding-orchestrator-specific features (2026-06-14, 752fe200, transcript).
- Dispatch eligibility: a node-run dispatches iff stale AND its wait-set is empty for the current frame; empty wait-set IS the any-of semantic (2026-05-14, subscription-cascade, artifact).

## Intentional absences

- **Any general operator verb to invalidate or re-run arbitrary nodes** — POST /v1/nodes/{id}/invalidate and its admin sibling, CLI subcommand, node:invalidate permission, and the synthetic-envelope frame-creation path are retired; operators post template-declared messages; ad-hoc force-stale is debug-override only (paused/breakpoint) (2026-06-15, 91ec93d1 and 4c42fe5b, transcript): "re-running arbitrary nodes breaks the model."
- **Ad-hoc invalidation of parked nodes** — parked is a legitimate waiting state, not a degenerate case; neither parked nor non-parked nodes may be user-invalidated; wake happens through real mechanisms (2026-06-16, 4c42fe5b, transcript).
- **Harness.InvalidateNode and a test-only state-injection helper** — the helper was itself the principle violation; tests drive invalidation via declared messages/subscriptions and real park/async paths; TestOperatorInvalidateTargetOnly and TestParkedLifecycleResumeOnExternalInvalidate retired with their subject (2026-06-16, 4c42fe5b, transcript).
- **A synthesized node "state" field on the read API** — deliberately removed (NodeRow.State, SettlingSignalType, AssignedSupervisorID, InFlightRunID, RunScopeID, the LATERAL JOIN and fan-out scope-priority heuristic); restoration was later explicitly rejected and tests re-pointed at run_summary (2026-06-20..2026-06-29, 8a3b8c19/10cf843b/8a8539a4, transcript).
- **A 'scheduled' node state** — the resolution flavor lives in a separate column, never a fifth state of the old machine (2026-05-05, reactive-loops, artifact; the whole node-state machine is itself now historical, see below).
- **on_executor_blocked slot** — removed; all error variants flow through on_executor_errored discriminated by error_class; the dead TemplateSpec field and TerminalKindBlocked constant were also deleted (2026-05-12..2026-05-13, nomenclature-resolution, artifact).
- **Post-processors as a platform feature** — deterministic transformations of agent output are downstream nodes; no transformation-registry primitive (2026-05-08, platform-extensions, artifact).
- **by_node userdata defaults** — rejected as redundant; inheritance-by-reference (abstract base nodes) considered and deferred (2026-05-19, multi-instance-template-ergonomics, artifact).
- **The dependencies: block** — retired; decomposed into substitution refs (auto-subscribing read access), explicit subscribes:, and the wait-set ledger (2026-05-14, subscription-cascade, artifact).
- **Any-node message emission** — rejected in favor of the dedicated emitter node-kind (visibility as a graph object; multi-source composition needs subscriptions) (2026-06-19, 8a3b8c19, transcript).
- **The emits_message special-case weld** — the pre-in-proc-executor branches in dispatch, terminal-resolution, and scheduler (plus the sub-graph-entry panic guard) are replaced by the standard dispatch interface (2026-06-19, 8a3b8c19, transcript).
- **A separate parked-node:wake action** — (historically) wake was node:invalidate's job (2026-05-15, artifact); with operator invalidate retired, no operator wake surface exists at all — park exits via real mechanisms (time wake, in-graph invalidate, watchdog) (2026-06-16, transcript).
- **A "special" message-payload substitution path** — messages substitute through the identical node-attribute code path; the gate-evaluator messages-into-deps loop was drift (2026-06-22, 10cf843b, transcript).
- **Removal of existing functionality (Park, error-policy retry, ResumeContext) as part of the utility-node work** — explicitly additive scope; nothing existing had to be removed then (2026-06-14, 752fe200, transcript).

## Corrections and restorations (drift-fight record)

- Node state fully removed, then found re-synthesized: the operator API was presenting a state field rebuilt from the latest run after the removal ("thought we explicitly removed this"); removed again — precedent that any state-like dual surface on the node row/API is drift (2026-06-21, 10cf843b, transcript). Same pattern re-rejected 2026-06-29 (test polling node.state).
- "Didn't we get rid of any notion of node state?" — user directed verification that the removal landed; fresh must never be synthesized for run-less nodes (2026-06-21, 10cf843b, transcript).
- Message substitution had drifted into a parallel side-path (gate-evaluator messages-into-deps loop); user ruled messages are literally an alias for nodes at runtime and directed checking the implementation and design history (2026-06-22, 10cf843b, transcript).
- Harness.InvalidateNode framing corrected: not a surface needing migration but a principle violation to delete; parked nodes equally ineligible (2026-06-16, 4c42fe5b, transcript).
- Invalidate-conflict rule was mis-stated in code and docs (claimed "valid only for parked or fresh"); actual rule: rejected only for running (2026-06-02, rimsky-core-remediation, artifact) — the whole surface was then retired 2026-06-15, so this correction is historical context, not a live rule.
- Public docs drifted to four node states when the canonical count was five (parked missing) (2026-05-13, nomenclature-resolution, artifact) — itself now historical under the no-node-state model.
- The retired on_executor_blocked slot survived as a dead TemplateSpec field after the first dispatch; fully deleted in cleanup accepting the template-hash change (2026-05-13, artifact).
- The kind:/type: field-name collision was caught in review — the sugar field became `kind:` because `type:` was already the routing key (2026-06-14, 752fe200, transcript).

## Superseded / historical

- Foundation two-bits-plus-annotation node state (has_value / has_outstanding_request / auto_recovers), the four-state fresh/stale/running/failed presentation, the five-state (parked) correction, and the last_outcome column with its fresh_changed cascade gate (2026-05-04..2026-05-13, artifact) → all superseded: state belongs exclusively to node_runs (whose vocabulary grew pending/held), the settling signal taxonomy replaced last_outcome (stale slug last-outcome → signal, 2026-05-25 artifact), and the node row carries no state (2026-06-20+, transcript).
- Split ownership of rimsky_nodes columns across foundation/modeling incl. frame_id (2026-05-04, artifact) → node rows immutable during frames; frame_id removed (2026-07-05, transcript).
- Four lifecycle-handler slots (2026-05-05) → three slots + on_event (2026-05-12).
- Claim-unavailable pass via on_acquire_unavailable with last_outcome=passed and the "loop until nothing left" pattern (2026-05-05, artifact) → the composable-loop goal survives via utility nodes/messages; the last_outcome vocabulary it was expressed in is retired.
- node:invalidate covering parked wake (2026-05-15, artifact) → operator invalidate retired entirely (2026-06-15).
- 2026-06-08 corpus promises of operator force-invalidate and in-cascade invalidate → superseded by the messaging-only model and breakpoint-gated debug channel (2026-06-15..16, transcript).
- node/reset as reset-plus-wake → narrowed to pure retry-budget clear (2026-06-16).
- GET /nodes/{id} settling_signal_type field (2026-06-06, artifact) → briefly removed with the derived-state sweep, then restored the next day (2026-06-22, 2250e8aa) as a per-run projection, not a synthesized node state; it remains on the response today, distinct from the retired generic `state` field (2026-06-06..2026-06-22, corpus-bootstrap/derived-state-sweep, artifact).
- Message emission as a node dispatch mode via emits_message: declaration (2026-06-14, transcript) → reimplemented as the rimsky.emit_message utility node with the sugar canonicalized away (2026-06-19); the emits_message mechanism itself later appears on the excision list (2026-07-07, recorded under frame/signal concepts).
- The proposed default empty-type root message node injection sketch (2026-06-15, 91ec93d1, transcript) — captured as a deferred sketch, not a landed promise.

## Conflicts needing human ruling

- None. Apparent contradictions (operator invalidate promised 2026-06-08 vs retired 2026-06-15; node state five-state docs vs no-node-state) all resolve by later-supersedes-earlier and transcript-over-artifact.
