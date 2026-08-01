# Intent Dossier: lifecycle-subscriber

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- LifecycleSubscriber is a standalone service protocol (split out of the Store protocol) with seven callbacks: OnTemplateRegistered / Deployed / Undeployed / Deregistered, OnInstanceCreated / Terminated, and OnRunScopeTerminal (6→7, 2026-05-24, host-agent-and-proxy, artifact).
- Scope is the control-plane / instance lifecycle only; it deliberately does NOT carry node-cascade events (individual node-run transitions such as parked), which belong to signal / event-log — an intentional boundary (2026-06-06, comprehensive-gap-closure, artifact).
- Lifecycle is universal across peer kinds (claim-producer, executor, publisher, sensor); the template slot says how the template uses a service, the protocols list says what it can do — the axes must never be conflated (2026-06-19, 08d65bfe, transcript).
- Delivery is idempotent, keyed (peer-name, event-type, object-id) in rimsky_lifecycle_idempotency; replays are no-ops; subscribers return nil from methods they don't react to; one binary may implement zero, one, or multiple protocols (2026-05-04, layer-crystallization / service-protocol-contract, artifact).
- There is no separate lifecycle_subscribers: YAML block — a peer declares the protocol via the protocols: list on its primary block (2026-05-04, layer-crystallization, artifact).
- Events fire from the rimsky-side process that owns the state transition's post-commit fan-out: control-api for template/instance events and main-scope run-scope-terminal; the supervisor (via its own registry and a LifecyclePeersForSpec function pointer respecting runtime-purity) for sub-graph / fan-out-partition run-scope-terminal (2026-05-24, host-agent-and-proxy, artifact).
- Under frame-owned run scopes, an instance that has never run a frame has no run scope to close, so terminating it correctly does not fire OnRunScopeTerminal; automatic instance termination and the per-instance-scope model are retired (2026-07-07, 3f71f90a, transcript).
- Non-subscribed peers referenced by a template are silently skipped during fan-out (no idempotency row, no error) — the fail-loud policy died with the bundled-into-Store era (2026-05-04, layer-crystallization, artifact).

## Required behaviors (open promises)

- Seven callbacks fired synchronously at the corresponding transition, carrying the documented context (template hash, instance ID, run-scope ID, service bindings, owner key, terminal reason); the subscriber's failure response is honored synchronously at the close site, not fire-and-forget (2026-06-08, corpus-bootstrap, artifact).
- Idempotent delivery keyed (peer-name, event-type, object-id); replay is a no-op (2026-05-04, service-protocol-contract, artifact).
- OnInstanceTerminated fires exactly once, on the terminated_at NULL→timestamp transition (2026-05-04, modeling-layer-contract, artifact).
- Instance creation is idle and fires exactly one OnInstanceCreated: POST /instances creates the row as the entire effect — no frames, no messages, no node-runs; the supervisor dispatches nothing until a message lands (STORY-instance-create-is-idle; 2026-06-16, 4c42fe5b; proven e2e and signed off 2026-06-17, b95ff4a7, transcript).
- The fan-out peer set is the template's flat reference set across ALL peer slots — including publishers/sensors — with the lifecycle-subscriber protocol filter applied downstream; regression-tested (2026-06-19, 08d65bfe / 8a3b8c19, transcript).
- OnInstanceCreatedRequest carries service_bindings (bytes) and owner_api_key_id (empty for anonymous) as proto3-additive fields; existing subscribers receive empty defaults unchanged (2026-05-24, host-agent-and-proxy, artifact).
- Ordering at instance termination: on_run_scope_terminal fires before on_instance_terminated, at both the polling-terminator and explicit-DELETE close sites (2026-05-24, host-agent-and-proxy, artifact) — noting the 2026-07-07 reversal means this ordering applies only when a run scope exists to close.
- Lifecycle events' wire payload key is template_hash (content-hash intent) (2026-05-04, layer-crystallization, artifact).
- Bundled store services ship no-op LifecycleSubscriber implementations behind an opt-in enable_lifecycle flag so operators can wire the protocol without forking (2026-05-04, layer-crystallization, artifact-only). The bundled postgres store's deployed callback is a documented no-op fork-point — DDL-on-deploy is the archetype the protocol enables, not shipped behavior (2026-06-06, comprehensive-gap-closure, artifact).
- The lifecycle fan-out peer filter includes configured late_bind_service_proxies names when a template has late_bind_services, scoped to instance- and run-scope-keyed fan-out only (not template events) (2026-05-24, host-agent-and-proxy, artifact-only).
- An in-tree canary scenario exercises the lifecycle-subscriber callback contract on every PR (2026-05-24, repo-reorganization, artifact-only).
- Lifecycle tests wanting the OnRunScopeTerminal callback must drive a real frame (e.g. an empty wake message to a structural root) before terminating (2026-07-07, 3f71f90a, transcript).

## Intentional absences

- Bidirectional events from the peer back to rimsky (a peer cannot initiate) and cross-peer event-ordering guarantees (2026-05-04, service-protocol-contract, artifact).
- A lifecycle/* top-level signal kind on node subscriptions — declined; it would create a parallel path with ambiguous semantics; the protocol already covers those use cases (2026-05-23, signal-taxonomy-and-policy-decoupling, artifact).
- An OnNodeParked lifecycle event — explicitly deferred, not dropped (routing/idempotency/emission-site questions open; gauge + diagnostics suffice) (2026-05-14, subscription-cascade-and-quality-of-life, artifact).
- A bundled watchdog runner — platform anomalies are foundation's job; domain watchdogs are project work built as a lifecycle-subscriber peer or a watchdog template (2026-05-08, platform-extensions, artifact).
- In-process bundling of lifecycle-subscribers — dropped from the all-in-one scope; nothing bundled speaks the callback protocol (openlineage is architecturally a Postgres-polling background worker, not a lifecycle-subscriber) (2026-07-03, 8a8539a4, transcript).
- Fail-loud fan-out to non-subscribed peers — superseded by silent skip under the per-peer-protocols model (2026-05-04, layer-crystallization, artifact).
- OnRunScopeTerminal on terminate of a never-ran instance — correctly absent under frame-owned run scopes (2026-07-07, 3f71f90a, transcript).

## Corrections and restorations (drift-fight record)

- peersReferencedBySpec hardcoded only claim-producer and executor slots, structurally excluding publishers/sensors from lifecycle events — ruled "clearly a bug", fixed to walk spec.Publishers with a regression test; concept updated to state lifecycle is universal across peer kinds and slot vs protocol are orthogonal (2026-06-19, 08d65bfe / 8a3b8c19, transcript).
- The openlineage subscriber test's white-box coupling (importing rimsky persistence internals) was ruled out; the designed replacement was a public-API integration test, but execution shipped a vanilla-Postgres read-contract test instead — the rimsky→subscriber wire is no longer exercised end-to-end; recorded coverage loss, never retracted (2026-05-24, repo-reorganization + divergences, artifact).
- The proxy's OnRunScopeTerminal reap handler ID mismatch (instance id vs run-scope id) landed as a known no-op defect — recorded on the host-agent-proxy dossier; the lifecycle firing sites were correct, the consumer was not (2026-05-24, host-agent-and-proxy-divergences, artifact).
- The control-api main-scope close paths wrap in an explicit transaction (the plan's nil-tx snippet panicked) (2026-05-24, host-agent-and-proxy-divergences, artifact).

## Superseded / historical

- Lifecycle hooks bundled into the Store/ClaimProducer protocol → standalone LifecycleSubscriber service (2026-05-04, layer-crystallization / service-protocol-contract, artifact).
- Six methods → seven with OnRunScopeTerminal (2026-05-24).
- "All lifecycle events fire from control-api" invariant → firing-site-owns-the-transition (control-api + supervisor) (2026-05-24, host-agent-and-proxy, artifact).
- template_id payload key → template_hash (2026-05-04).
- rimsky_store_lifecycle table name → rimsky_lifecycle_idempotency (2026-05-04).
- The bundled OpenLineage deliverable as a lifecycle-event subscriber → a rimsky_lineage-polling reader with its own cursor (2026-05-15, data-platform-extensions, artifact); later characterized as architecturally not a lifecycle-subscriber at all (2026-07-03, 8a8539a4, transcript).
- Per-instance run-scope model where terminate always closed a scope and fired OnRunScopeTerminal → frame-owned run scopes (2026-07-07, 3f71f90a, transcript).

## Conflicts needing human ruling

None recorded — the precedence rules resolve the record's tensions on this concept.
