# Intent Dossier: template

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Template identity is a content hash: sha256 over the RFC 8785 JCS-canonicalized spec bytes, computed over *resolved* bytes (post `source_file` inlining). Re-registering the same spec is idempotent; consumers re-resolve by tag rather than caching hash strings. The vocabulary is `template_hash` everywhere; `template_id` is dead.
- Tags are movable aliases onto hashes; rebinding a tag atomically redirects future instance creation and never migrates or affects live instances.
- Cascade coupling is declared receiver-side via `subscribes:` entries carrying two **required** booleans, `wake_on_change` and `force_upstream_refresh` — no defaults; registration rejects an entry missing either. Substitution refs generate **no** implicit edges; a registration-time coverage check requires every substitution ref to be matched by a covering subscription.
- The substitution grammar has exactly six source kinds under one repo-wide syntax and one pipeline: `claim`, `params`, `nodes`, `messages`, `child`, `env`. `messages` is a lexical alias for `nodes`, type-checked at registration. Whole-directive inputs return the resolved JSON value as-is; embedded directives stringify.
- Frame-resolution mode no longer exists as a template surface (coalesce retired; at most one running frame per instance). The message-queue knob is `message_queue_mode` (`backlog` default | `coalesce`), declared on the template as a default and copied into each instance row at creation.
- Error policy is simplified: each error class maps to a single action (`retry` | `give_up` | `pass` | `release_and_requeue`); a single node-level `MaxRetries` (plus node-level `RetryBackoff`) bounds retries across all classes; undeclared classes default to `give_up`. Operator `error_types` keys range-check against the executor's advertised `declared_error_classes`.
- `cascade_mode` is a per-node template field with four values (most-recent, sequenced, idempotent-queue, idempotent-settled), default most-recent, one mode per node.
- Attributes replaced userdata. Carry-forward is default-on for all properties. If a node has attributes they must have a schema; the effective schema merges the executor's `expected_attributes_schema` with template defaults and the node schema, and the executor is authoritative (template cannot relax `additionalProperties` or claim `readOnly` the executor doesn't).
- Registration-time reference validation is an explicit operator mode `templates.ref_validation_mode: all` (default, strict) | `available` | `none`; instantiation (POST /instances) is the mandatory validation gate for whatever a relaxed mode skipped.
- Retired template surfaces are erased completely: no detection code, no migration-redirect errors, no "previously" prose — retired shapes fail through generic paths (unknown field, taxonomy enforcement, registry lookup). (Two later, specifically-ratified exceptions exist — see Conflicts.)
- A node finds its executor by its `type:` field matching the executor's name in rimsky.yml; `kind:` is the sugar field for bundled utility nodes (declaring both `kind` and `executor` is a registration error). The same template file runs byte-identically under all-in-one and containerized deployments.
- The empty message type `""` is reserved for the runtime: every template's declared-types set gets an implicit `""` entry with a null body schema; author-declared `""` message entries or nodes are rejected at registration; an empty message wakes every structural root through the universal typed-message path with no special-case branch.
- Templates must let sophisticated multi-loop orchestrator patterns be expressed through generic composable primitives only — no use-case-specific features.
- The per-node claim directive is `claim_producers:` (the stores→claim-producers rename is complete across template DSL, proto, routes, config, images).
- Design principle for the authoring surface: defaults can render features invisible to AI agents authoring templates, so default-on behaviors need explicit surfacing (docs, registration output, canonical example schemas).

## Required behaviors (open promises)

Identity and registration:
- Content-hash identity with idempotent re-registration; hash bytes not pinned pre-v1 (2026-05-04, modeling-layer-contract, artifact): "consumers MUST re-resolve by tag rather than caching hash strings."
- Any string-valued position in a template may be `{source_file: <relative-path>}`; the CLI resolves at register time, single pass, server sees only resolved bytes (2026-05-19, multi-instance-template-ergonomics, artifact). Resolved paths must stay inside the template directory subtree; escapes are security errors with CLI exit 2 (same source). The content hash is over resolved bytes (same source).
- Registration accumulates **all** validation errors and returns the full `validation_errors` array — it does not bail on the first (2026-05-28, quality-of-life-features, artifact): "the prior report's claim that registration 'bails on the first error' is **false**."
- Template lint: `POST /templates/validate` + `rimsky template lint <file>` run the full registration pipeline without persisting; validate is a read action orthogonal to dry-run; HTTP 200 even with findings; CLI exit codes 0 clean / 1 drift / 2 usage error (2026-05-28, quality-of-life-features, artifact).
- Registering a recursive (delegate-cycle) sub-graph template is rejected 400 `subgraph_recursion_unsupported` (2026-06-02, acceptance-coverage-recovery, artifact).
- RefValidationMode `all` (default) | `available` | `none` spanning all service types (2026-06-06, comprehensive-gap-closure, artifact); rejection messages name the active mode and the `templates.ref_validation_mode` key (2026-06-11, last-mile-stability, artifact): "an error that hides the knob defeats the feature."
- Instantiation is the mandatory validation gate — statically-knowable node attribute config validated against every referenced service's schema, value constraints included, before the instance runs (2026-06-06, comprehensive-gap-closure, artifact).
- Template lifecycle: register, get by name or hash, validate-without-persist, deploy (gates instance creation), undeploy (refuses new instances), delete only when unreferenced (409 while referenced) (2026-06-08, corpus-bootstrap, artifact).
- Unknown utility-node `kind:` values are rejected at registration exactly as unknown executors are (2026-06-14, 752fe200, transcript).
- Unknown `cascade_mode` values rejected at template-parse time with a field-level error naming the four valid options; empty defaults server-side to most-recent (2026-06-22, 10cf843b, transcript).
- Registration rejects subscriptions whose tag predicate references a tag not in the emitter's `ObservabilityCapabilities.declared_tags`; the supervisor's terminal handler is the second gate, rejecting settling outcomes carrying undeclared tags (2026-06-16, 055468fc, transcript) — the runtime gate was explicitly ratified as a plan step "so it would not be silently dropped."
- `agent/…`-style error classes an operator routes in `error_types` must be range-checked against the executor's advertised `declared_error_classes` (2026-06-04, claude-agent-signoff-gate, artifact).
- Registration cross-checks `{{params.<key>}}` refs in tags against ParamsSchema; non-params directives in tags rejected (2026-05-19, multi-instance-template-ergonomics, artifact) (artifact-only).

Subscriptions and substitution:
- Both subscription flags required on every entry, no defaults (2026-06-14, 37e2ea5e, transcript): "forcing them to understand what to specify instead of relying on documentation and a silent default."
- An uncovered substitution ref fails registration with structured error kind `substitution_ref_uncovered` naming receiver, ref text, property path, plus a copy-pasteable suggested `subscribes:` entry (2026-06-14, 37e2ea5e, transcript).
- Subscription cycles between nodes are legal (defer-loop across frames under the wait-set model); no deploy-time cycle rejection (2026-05-14, subscription-cascade-and-quality-of-life, artifact).
- `{{messages.<type>}}` routes through the same Deps lookup as `{{nodes.<type>}}`; undeclared message refs rejected symmetrically with nodes refs (2026-06-19, 8e7e4c10, transcript); one parser/reference-type/extractor/resolver, no runtime branching (2026-06-19, 8a3b8c19, transcript): "'messages' is an alias for 'nodes' and we typecheck at registration."
- `{{env.VAR}}` is a substitution source kind: dispatch-time non-graph input like params — no wait-set rows, no edges, no cascade coupling; scoped to non-secret configuration (secrets travel executor-side via allowlist) (2026-06-24, 3b1066c7, transcript; 2026-07-01, 8a8539a4, transcript).
- Whole-directive substitution returns the resolved JSON value type-preserved (number 42, not "42") (2026-05-19, multi-instance-template-ergonomics, artifact).

Attributes:
- Carry-forward default-on with visibility mitigations: concept doc documents readOnly-plus-writeback as the canonical stateful pattern, registration surfaces stateful properties, shipped schemas carry describing text (2026-06-14, 752fe200, transcript): "default-on with the mitigations."
- Attributes always have a schema; values do not carry forward across run boundaries beyond their RunScope; a template version cannot change during a run (2026-06-15, c60b550a, transcript).
- `checkAttributesSchema` registration rule: each property has at most one of `source:`/`default:` and must have source, default, or executor-side `readOnly: true`; violations rejected `template_validation_failed` (2026-05-20, userdata-collapse-into-attributes, artifact).
- Template-level defaults `defaults.attributes.by_executor` merge under node declarations and above nothing — merge order template defaults → node → instance overrides by_executor → by_node, objects deep-merge, arrays replace; validators inspect routing keys only, never fragment values (2026-05-19, multi-instance-template-ergonomics, artifact; carried through the 2026-05-20/21 attribute collapse).

Node and graph features:
- Utility nodes: a template references a bundled node kind (e.g. loop_counter) and it dispatches with no external service registered and no extra deployment (2026-06-14, 752fe200, transcript).
- Nodes support a `tags:` list for dashboard/events filtering (2026-05-19, crimefinder, artifact) (artifact-only).
- Templates gain top-level `late_bind_services: [...]`; listed names bypass registration-time existence and schema checks, unlisted names stay strict; empty default = today's strict behavior; the field lives in canonical-spec bytes (with omitempty so absent field keeps old hashes) (2026-05-24, host-agent-and-proxy, artifact) (artifact-only).
- Sub-graphs: top-level `graphs:` block, `main` reserved, entry/exit nodes, invoked via `delegate:` (mutually exclusive with `executor:`) (2026-05-15, data-platform-extensions, artifact); bounded iteration is a pattern (N static delegate nodes), never a loop construct (2026-05-19, crimefinder, artifact).
- Fan-out declared per-node (`fan_out:` naming the claim, opaque partition_request, parallelism cap, error_policy strict/threshold/best_effort/first), gated at registration on the producer advertising `supports_split_scope` (2026-05-15, data-platform-extensions, artifact); the seven 2026-06-18 fan-out stories are locked, including partition_request from any substitution source and `{{claim.<alias>.payload}}` resolving for children as for regular claims (2026-06-18, 9fb55f08, transcript).
- Per-node `max_park_duration` cap field (top-level node field), unset = indefinite park (2026-05-08, platform-extensions-for-agent-consumers, artifact; corroborated 2026-06-15, 8c66c02c, transcript — per-reason fallback awaiting promotion).
- Per-node executor silence detection config, defaulting to zero (disabled), matching the per-node deadline trio pattern; replaces the global env knob (2026-06-24, 8a8539a4, transcript): "in general, we favor defaulting timouts to zero and instead implementing progress-based keepalive."

Empty-message trigger:
- Implicit `""` message type seeded at registration with null body schema; author-declared `""` rejected; receipt handler has no empty-case branch; structural-root edges auto-injected under sender `""` (wake_on_change true, force_upstream_refresh false) into the cached inverse-edge map — canonical hash unchanged; auto-injection skips nodes with any subscribes entries (2026-06-16, 4c42fe5b, transcript; reconfirmed binding 2026-07-03, 3f71f90a, transcript). The `""` receiver NodeRow is appended at instance creation, not seeded into the persisted spec (2026-07-05, 3f71f90a, transcript).
- Empty message post wakes every structural root in exactly one frame; Idempotency-Key replay returns 200 with the original message id and opens no second frame (2026-06-17, b95ff4a7, transcript).

Onboarding, portability, and delivery:
- A runnable example TemplateSpec ships in examples/ and `rimsky run <file>` really performs register + deploy + instantiate against a live stack and reaches terminal; the README invocation works as written (2026-06-06, comprehensive-gap-closure, artifact; 2026-06-08, corpus-bootstrap, artifact).
- `rimsky run <template>` self-hosts by default (in-process all-in-one stack, bundled handlers, one-shot, zero-config); `--endpoint` keeps remote behavior; no template/manifest autodetect (2026-07-01, 8a8539a4, transcript; 2026-07-04, 3f71f90a, transcript).
- Zero-config local orchestration drives to terminal against real bundled services doing real work — stubs/canned replies are falsifiers (2026-07-03, 8a8539a4, transcript).
- Same template bytes run unchanged all-in-one ↔ containerized; a portable-template cross-mode proof asserts identical terminal-graph shape (2026-07-01 and 2026-07-04, 8a8539a4/3f71f90a, transcript): "we'd like users to not have to change their templates if moving from all-in-one to containerized."
- Compose (`rimsky compose up/plan/status/down`) reconciles templates/tags/instances declaratively, namespaced under `compose:<project>:`, no docker/kubectl shelling, nothing stubbed (2026-06-08, corpus-bootstrap, artifact).
- In-tree canary scenario tests exercise template registration + a run through the control-api YAML grammar on every PR (2026-05-24, repo-reorganization, artifact) (artifact-only).
- Publishers: template block is `publishers:` (formerly `sensors:`), config resolved by substitution from instance params; watches start/stop with the instance (2026-05-15 + 2026-05-17, data-platform-extensions / sensor-messaging-unification, artifact).
- Sequenced cascade-mode per-round substitution restoration: each round-driving sender resolves from its pinned wait-set sender_run_id, falling back to most-recent-settled only for non-driving subscribed senders (2026-07-13, 3f71f90a, transcript, committed 2d4952e4).

## Intentional absences

- `dependencies:` node block — retired 2026-05-14 (subscription-cascade-and-quality-of-life, artifact); decomposed into substitution refs, `subscribes:`, wait-set.
- Lifecycle-handler slots (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`) and the propagate-resolve vocabulary (`by_changed`/`always_propagate`/`never_propagate`) — retired entirely 2026-05-23 (signal-taxonomy-and-policy-decoupling, artifact); cascade-fire is subscriber-driven, senders cannot suppress downstream firing. (`on_executor_blocked` was already deleted 2026-05-13.)
- `on_event:` handler map — retired 2026-05-19 (crimefinder, artifact); consumption is via subscriptions.
- `frame_resolution` / `frame_resolution_mode` and the coalesce frame mode — dropped entirely 2026-06-14 (bfc9febb, transcript): "the work here is to remove coalesce and limit direct invocation." Message-layer coalescing lives in `message_queue_mode` instead (2026-07-05).
- `hard_dep` flag AND any code detecting it — declined 2026-06-14 (37e2ea5e, transcript): "don't want to commemorate it. erase it completely instead." General rule: retired features leave no remnants, no fail-loud detection (2026-06-14, bfc9febb, transcript).
- Implicit cascade edges from substitution refs — dropped with Approach A, 2026-06-14 (37e2ea5e, transcript).
- `userdata:` vocabulary (per-node blocks, `userdata_overrides`, `defaults.userdata`) — collapsed into attributes 2026-05-20/21 (userdata-collapse-into-attributes, artifact); templates carrying `userdata:` are rejected.
- `by_node` template-level defaults — rejected as redundant with node-level declaration; inheritance-by-reference (abstract base nodes) considered and deferred (2026-05-19, multi-instance-template-ergonomics, artifact).
- `{{deps.X.Y}}` grammar — replaced by `{{nodes.X.attribute.Y}}` with no compat alias (2026-05-14, artifact). `{{userdata.*}}`/`{{instance.*}}` never existed by design (2026-05-19, crimefinder, artifact). `{{trigger.message.*}}` — retired 2026-06-19 (8e7e4c10, transcript). `nodes.<X>.event.<name>.<path>` source kind — dropped 2026-06-17 (b31002b8, transcript); per-emission data rides attributes.
- `payload_template` pre-shaping — dropped 2026-05-17 (sensor-messaging-unification, artifact): "doesn't pay for its complexity."
- `sensors:` template block name — renamed `publishers:` 2026-05-17 (artifact).
- `stores:` as the per-node claim directive and everywhere else — renamed `claim_producers:` 2026-06-19 (a02fe167, transcript): "rename them all."
- Template-level default for `terminate_after_run` — declined, per-instance only (2026-06-03, instance-lifecycle-durable-by-default, artifact).
- Registration-time detection/warning of shell-style `${VAR}`/`$VAR` — declined (2026-06-24, 3b1066c7, transcript).
- Author override of the empty-message entry point (author-declared `""` node) — out of scope, reserved for runtime (2026-06-15, 4c42fe5b, transcript).
- The implicit always-on soft-fail reference-validation heuristic — replaced by explicit RefValidationMode (2026-06-06, comprehensive-gap-closure, artifact).
- Pull-vocabulary rename of `subscribes`/`payload` (e.g. `watch`/`body`) — considered and declined; subscribe is standard reactive vocabulary (2026-07-07, 3f71f90a, transcript).
- `type:` as the utility-node sugar field name — it collides with the existing required routing-key field; the sugar is `kind:` (2026-06-14, 752fe200, transcript).
- Use-case-specific orchestrator features (e.g. for coding orchestrators) — forbidden; generic primitives only (2026-06-14, 752fe200, transcript).

## Corrections and restorations (drift-fight record)

- Published examples drifted into a fictional template DSL; realigned to real shapes (top-level fields, `nodes:` as a list with `type:`, no `template:` wrapper); examples must be copy-pasteable and runnable verbatim (2026-05-04, public-docs-architecture, artifact).
- The retired `on_executor_blocked` slot survived as a dead TemplateSpec field still validated and serialized; fully deleted, accepting the hash change (2026-05-13, nomenclature-resolution, artifact).
- `stores:` vs `claims:` doc-vs-code drift was documented and lingered from 2026-05-19 until the user ordered the full rename to `claim_producers:` on 2026-06-19 — precedent that a stalled rename is drift to finish, not tolerate.
- YAML keys `cancel_siblings:`/`max_failures:` were silently dropped (json-only struct tags); yaml tags added; the bot PR offering the fix was explicitly not merged (2026-06-02, rimsky-core-remediation, artifact).
- A consumer report claimed registration bails on first error — refuted; the real gap was validate-without-persist, which was then built (2026-05-28, quality-of-life-features, artifact).
- Broken cascade-mode/seal story proofs used posted messages to generate cascade rounds (forbidden by one-message-one-frame); rewritten to in-template cascade self-edges bounded by CEL `when:` (2026-07-03, 3f71f90a, transcript).
- Sequenced per-round substitution was flattened by commit bc3280d7; restored immediately (pinned sender_run_id per round) while keeping that commit's genuine fix (template-declared subscriptions as the substitution sender set) (2026-07-13, 3f71f90a, transcript).

## Superseded / historical

- `frame_resolution` required field (2026-05-04) → renamed `frame_resolution_mode` (2026-05-12) → retired with coalesce (2026-06-14, transcript).
- Four lifecycle-handler blocks (2026-05-04/05) → `on_event.<name>` map (2026-05-08) → on_event retired (2026-05-19) → whole lifecycle-handler surface retired into error-policy + signals (2026-05-23).
- Per-class error policy chains with counts and the retry-cap resolution order (row override → DSL `max_retries_without_progress` → deployment default → 100) (2026-05-08) → single action per class + node-level MaxRetries (2026-06-22, transcript).
- `subscribes:` entries with `on:` topic kinds and per-topic filters (2026-05-14) → two-required-flag model, no defaults (2026-06-14, transcript); migration values: existing entries wake_on_change=true/force_upstream_refresh=false, hard_dep-derived entries true/true (2026-06-15, b106a350, transcript).
- Executor userdata_schema validation at registration + dispatch (2026-05-08/11) and per-instance userdata overrides (2026-05-11) → attribute-schema model (2026-05-20/21).
- `permissive_warn` default for unreachable validators (2026-05-15) and silent-skip of declared-events checks when peers unreachable (2026-05-11/14/19) → explicit RefValidationMode, default strict `all` (2026-06-06).
- Any-string `error_types` keys (2026-05-19) → range-check against advertised `declared_error_classes` (2026-06-04).
- `declared_events` validation vocabulary (2026-05-14/19) → `declared_tags` under the tag-keyed cascade model (2026-06-16, transcript).
- PolicyAction keeping parse-compat `Targets`/`Frame` fields through a retirement window (2026-05-23) — superseded by the erase-completely rule (2026-06-14, transcript).
- `ParkRequested`→`Snooze` wire rename (2026-05-12) reverted to `Park` (2026-05-13) — template-visible vocabulary stayed park-flavored.
- Registration-time validation pipeline ordering + warnings-as-errors escalation (2026-05-15) — subsumed by the mode-based validation model (2026-06-06).
- The empty-`""`-type seeding into registration-time caches only (2026-06-16) refined: receiver NodeRow appended at instance creation, persisted spec and hash untouched (2026-07-05).
- Story most-recent-coalesces-cascades → retired in favor of the message-queue-layer story when `message_queue_mode` landed (2026-07-05).

## Conflicts needing human ruling

- **Erase-completely vs. ratified migration errors.** The general rule (2026-06-14, transcript) forbids any code detecting retired shapes or naming them in errors. Yet two later transcript-ratified decisions do exactly that: the retired event-substitution form is rejected "with a load-bearing migration message" telling authors to rewrite as terminal/* + payload.tags CEL (2026-06-17, b31002b8), and the legacy top-level `stores:` YAML key "stays hard-rejected with an error directing rename to claim_producers" (2026-06-19, a02fe167). Later-specific plausibly beats earlier-general, but the record never reconciles whether these two carve-outs are permanent or should eventually fall to the erase rule.
