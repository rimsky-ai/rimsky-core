# Intent Dossier: instance

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- **Instance creation is inert/idle**: POST /instances writes the instance row, unpaused, and nothing else — no frame opens, no envelope is written, no root nodes auto-fire, no node-runs; exactly one OnInstanceCreated lifecycle-subscriber callback fires. Creating an instance has nothing to do with invoking it; invocation is always a message landing (2026-06-15, 91ec93d1, transcript: "we are saying: creating an instance has nothing at all do do with invoking it").
- **The wake path is an empty message**: posting an empty message (type "") opens a frame and stale-marks every structural root through the normal message-delivery path — no separate delivery code path, no synthetic messages. The empty type is reserved for the runtime; author-declared empty types are rejected at template validation. Mechanism: an empty-string receiver NodeRow is explicitly appended at instance creation alongside the author-declared message-receiver loop (persisted spec and template hash unchanged) (2026-06-15..2026-07-05, 91ec93d1 + b95ff4a7 + 3f71f90a, transcript).
- **Termination is operator-initiated only.** Auto-termination is scrubbed entirely: terminate_after_run, the MarkInstanceTerminatedIfDone write at frame settlement, and the column itself (dropped by migration) are all removed. An instance lives until force-terminate (2026-07-06, 3f71f90a, transcript: "we shuould just scrub this feature (auto-terminate)").
- **Terminate semantics**: the currently running frame finishes, then the instance stops — no new frames open, new messages are rejected, pending messages are cancelled. "Terminated" means disabled-and-ignore-future-messages with the row persisting — not undeploy, not delete (2026-07-06, 3f71f90a, transcript; the pending-message cancel adjudicated fix-code 2026-07-13, finding 437).
- **Terminate vs DELETE division of labor**: terminate makes an instance terminal (force-fails in-flight node-runs through the real lifecycle path, abandons their uncommitted in-flight claims, stamps terminated_at); DELETE remains the reaper — removes the row, releases held-durable claims, frees instance_key — permitted only once terminal. Terminate on an already-terminal instance is idempotent 200 (2026-05-28, quality-of-life-features, artifact; reconfirmed unchanged 2026-06-03).
- **Per-instance message queue with two modes**: `message_queue_mode` = backlog (default; queue grows, every message runs in sequence) | coalesce (discard all queued messages except the next). Declared on the template as a default, copied into each instance row at creation, per-instance overridable. Deliberately named away from the per-node cascade_mode vocabulary (2026-07-05, 3f71f90a, transcript).
- **At most one frame may ever be executing per instance**; frames have no queued state — a frame is created and then immediately runs; pending work waits as messages in the per-instance queue. The next message creates a new frame IFF no non-resolved (open or parked) frame exists for that instance (2026-07-05, 3f71f90a, transcript).
- **Frame state lives entirely in run scopes and node_runs**: nothing about frame execution or resolution changes the state of the instance row or any node inside it. At the start of a new frame, node_runs have only default attribute values (2026-07-05, 3f71f90a, transcript).
- **No per-instance main RunScope**: instance.MainRunScopeID is removed; each frame's start creates a fresh root RunScope owned by that frame (frame row carries root_run_scope_id). Terminating an instance that never ran a frame correctly fires no OnRunScopeTerminal (2026-06-30 + 2026-07-07, 8a8539a4 + 3f71f90a, transcript).
- **paused stays**: it simply pauses the instance and any in-flight frame; since frames are serial the in-flight/future distinction is moot (2026-06-15, 91ec93d1, transcript). Soft pause is the only pause semantics; pause/resume are non-idempotent at the API (409s); paused instances are never claimed by the supervisor; pause does not silence external publishers/sensors — their messages accumulate and drain on resume (2026-05-24, instance-debugger, artifact).
- An instance binds to the resolved template hash at creation and stays bound — tag movement never migrates live instances (2026-05-04, modeling-layer-contract, artifact).
- Instance-key uniqueness is per-template (UNIQUE(template_hash, instance_key), NULL keys distinct); idempotent re-create with the same key returns the existing row and ignores the request's flags (paused, and historically terminate_after_run) (2026-05-04 + 2026-05-24 + 2026-06-03, artifacts).
- **Instantiation is the mandatory validation gate**: statically-knowable node attribute config is validated against every referenced service's schema — value constraints included — and a violation refuses the create naming the offense; dispatch-time validation remains defense-in-depth (2026-06-06 + 2026-06-08, comprehensive-gap-closure + corpus-bootstrap, artifact).
- attribute_overrides routing: by_executor / by_node / by_match ({matcher, overlay} list, tiny equality-only grammar over node_type, executor, graph, child_key, attrs.<path>); validation inspects only routing keys, value bytes stay inert (2026-05-20/21, userdata-collapse + matcher-overlay, artifact). Match counts are **event-derived**: each match emits an attribute_override_matched event, the API aggregates at read time, and the persisted attribute_overrides_match_counts column is dropped (2026-07-06, 3f71f90a, transcript).
- `rimsky watch` supports `--until idle|terminated` with **idle** the default (idle = no open frame and no pending messages), backed by a ?pending=true message filter — replacing the retired wait-on-auto-termination behavior (2026-07-07, 3f71f90a, transcript).
- Compose one-shot (`rimsky compose run` / streamlined invocations): one invocation drives every declared instance to terminal inline, emits an empty message per instance after create, terminates explicitly via the control API after wait, exits with script-friendly codes 0/1/2/130 (2026-06-13/14/16, 65667e33 + f0176bde + 4c42fe5b, transcript).
- After messages-as-nodes, an instance's node listing includes materialized message-receiver nodes alongside user-declared nodes, while node_count reports the user-declared count (2026-06-22, 10cf843b, transcript).

## Required behaviors (open promises)

- STORY-instance-create-is-idle: POST /instances against a deployed template creates the row as the entire effect — empty frame collection, empty message ledger, no node-runs; OnInstanceCreated fires once; the supervisor dispatches nothing until a message lands. Proven e2e, user signed off (2026-06-16/17, 4c42fe5b + b95ff4a7, transcript).
- STORY-empty-message-wakes-roots: empty message with Idempotency-Key opens one frame (triggering_message_id = the envelope); every structural root stale-marks via the normal subscriber-side cascade; replay returns the original id and opens no second frame; N empty messages → N frames (2026-06-16/17, 4c42fe5b + b95ff4a7, transcript; "signed off").
- Every code path that creates an instance and expects it to run emits an explicit post-create empty-message wake: test harness, `rimsky run`, compose driver (deterministic compose-wake-<instance_key> idempotency key), parallel scaffolding — swept everywhere (2026-06-16, b95ff4a7, transcript).
- message_queue_mode backlog|coalesce honored on the per-instance queue; template default copied to instance at creation (2026-07-05, 3f71f90a, transcript).
- One-frame-at-a-time and message-gated frame creation (new frame IFF no open/parked frame) (2026-07-05, 3f71f90a, transcript).
- Force-terminate: running frame finishes; no new frames; further messages rejected; **pending messages cancelled via CancelPendingForInstance** — adjudicated fix-code, currently a latent leak (2026-07-06 + 2026-07-13, 3f71f90a, transcript, finding 437).
- Force-terminate frees a wedged instance full-stack: a real dispatch stuck running on an async callback transitions to failed (reason instance_killed) through the real lifecycle path, the run-scope closes, terminated_at is set, DELETE then allowed; deleting a non-terminal instance refused (2026-06-06 + 2026-06-08, artifacts; proven by scenario tests, not fakes).
- A parked node holds its enclosing frame open; an async-callback-pending node holds its frame as a structurally distinct property — each with its own dedicated e2e test (2026-06-17, b95ff4a7, transcript).
- `rimsky watch --until idle` (default) with the ?pending=true filter through persistence, control API, and CLI, conformance-covered on both backends (2026-07-07, 3f71f90a, transcript).
- Pause/resume: paused flag on create + POST pause/resume with 409 non-idempotency; GET projection surfaces paused; supervisor candidate query excludes paused with lock scoped FOR UPDATE OF d (2026-05-24, instance-debugger, artifact) (artifact-only).
- `rimsky instance kill` requires --force/--yes (exit 2 otherwise), optional --reason audited; instance:kill action with dry-run would_have_terminated envelope; `rimsky instance status` and `rimsky watch` are client-side aggregators over existing read endpoints (2026-05-28, quality-of-life-features, artifact) (artifact-only).
- Instantiation-gate validation including value constraints, refusing with a named violation, nothing persisted (2026-06-06/08, artifacts) (artifact-only).
- by_match overlays fold via DeepMergeJSON in declaration order after by_executor/by_node, pass the same schema gate; match counts readable via the API, event-derived (2026-05-21 artifact as amended by 2026-07-06, 3f71f90a, transcript).
- Node tags materialize {{params.<key>}} at instance creation; missing param fails creation with a typed error (2026-05-19, multi-instance-template-ergonomics, artifact) (artifact-only).
- service_bindings stored verbatim and opaque, exposed on GET /instances/{id} with created_by_api_key_id (sourced from IdentityFromContextOK.KeyID, never requestingKeyID) — the proxy's cache-miss fallback depends on both (2026-05-24, host-agent-and-proxy, artifact) (artifact-only).
- Publisher subscriptions as desired-state rows (mounting/active/failed/stopped) with per-subscription state on the instance detail so a mount failure is never silent (2026-06-11, last-mile-stability, artifact) (artifact-only).
- Template lifecycle gates instance creation: only deployed templates instantiate; undeployed refuse new instances; template delete 409s while referenced (2026-06-08, corpus-bootstrap, artifact) (artifact-only).
- The compose: prefix on tag/instance-key namespace is server-side reserved for the compose machinery (2026-06-08, corpus-bootstrap, artifact) (artifact-only).

## Intentional absences

- **terminate_after_run and all auto-termination** — scrubbed 2026-07-06 (3f71f90a, transcript), including the column (migration drop), story:terminate-after-run, and decision:instance-self-termination. This closes a three-step arc: unconditional auto-terminate-on-drain (drift, ruled 2026-06-03) → publisher-subscription-gated termination (symptom fix, reverted 2026-06-03) → opt-in terminate_after_run (2026-06-03) → removed entirely. Restorers must not resurrect any stage of it.
- **Per-instance main RunScope singleton** (instance.MainRunScopeID, from the 2026-05-22 fan-out-safety spec) — removed; frames own root scopes (2026-06-30, 8a8539a4, transcript).
- **Queued frames** — no frame 'queued' state exists; both queued-frame coalesce designs rejected (2026-07-05, 3f71f90a, transcript).
- **Synthetic messages / EnqueueSyntheticWakeFrame / the instance/root auto-trigger at creation** — retired; any implied trigger must be a real default message bundled with the operation (2026-06-15, 91ec93d1, transcript; deletion committed 7d71ef32, 2026-06-17).
- **attribute_overrides_match_counts persisted column** — dropped (migration 020); event-derived instead. Frame processing writes only node_runs and the event log, never the instance row (2026-07-06, 3f71f90a, transcript).
- **A start/wake convenience flag on create, or a separate start verb** — declined; pure two-step flow (create, then empty message) (2026-06-15, 4c42fe5b, transcript: "A").
- **Hard pause** (preempting in-flight dispatches) — rejected as a primitive; soft pause + empty-matcher pause-breakpoint composes it (2026-05-24, instance-debugger, artifact).
- **Bulk-instance manifest CLI subcommand** — declined; at that scale bulk loaders, not YAML manifests (2026-05-19, multi-instance-template-ergonomics, artifact).
- **Cross-instance RunScope linking** — out of scope; cross-instance coordination uses messages/triggers (2026-05-22, fan-out-safety, artifact).
- **kill --purge chaining the delete** — out of scope; operator follows with instance delete (2026-05-28, artifact).
- **A template-level default for terminate_after_run** — declined (2026-06-03, artifact); mooted by the feature's removal.
- Retired mechanisms that must not appear as current anywhere: stored rimsky_frames.state, terminate_after_run, the per-instance main RunScope, queued frames, wait_set.subscription_scope (migration 016), attribute_overrides_match_counts (migration 020) (2026-07-07, 3f71f90a, transcript: "the things we're trying to excise").

## Corrections and restorations (drift-fight record)

- **Auto-terminate-on-drain was ruled drift** (2026-06-03, instance-lifecycle-durable-by-default, artifact): MarkInstanceTerminatedIfDone at every frame-end made every instance a batch job, contradicted the durable model, and was never in the concept doc. First corrected to durable-by-default + opt-in flag; the flag itself was then scrubbed (2026-07-06, transcript). Strong precedent: termination is independent of sensors, publisher-subscriptions, and node presence.
- **Publisher-subscription termination gate** (2026-06-02 acceptance-coverage fix) — explicitly reverted 2026-06-03 as a symptom patch binding termination to sensors, exactly the coupling the durable model forbids.
- **Terminate path never updated for the July-5 queue redesign** — lifecycle events cancel pending messages but terminate didn't; adjudicated fix-code, CancelPendingForInstance (2026-07-13, 3f71f90a, transcript, finding 437).
- **Nothing in production ever set terminated_at** before the terminate feature — instances could never be terminated or removed; POST /instances/{id}/terminate was the first production teardown path (2026-05-28, quality-of-life-features, artifact).
- **SetPaused CTE race** — two concurrent SetPaused(true) both saw prior=false, hiding the 409; replaced with SELECT FOR UPDATE + UPDATE (SQLite: BEGIN IMMEDIATE) (2026-05-24, instance-debugger divergences, artifact).
- **GET /instances/{id} omitted service_bindings / created_by_api_key_id** the proxy fallback needed — response extended (2026-05-24, host-agent-and-proxy divergences, artifact).
- **Instance delete had to cascade at runtime** through the run-scope tree because the FKs lack ON DELETE CASCADE (2026-05-22, fan-out-safety divergences, artifact).
- The empty-wake design was restated by the user as the original decision after implementation wandered: an injected empty-string node at instantiation, "no special additional code," one code path (2026-07-05, 3f71f90a, transcript).

## Superseded / historical

- Auto-terminate-on-drain → durable-by-default + terminate_after_run (2026-06-03) → auto-termination scrubbed; operator-initiated only (2026-07-06, transcript).
- Instance creation auto-triggering roots via synthetic envelope → creation inert + empty-message wake (2026-06-15, transcript).
- FrameDeliveryMode default coalesce (2026-05-15) → serial_queue default (2026-05-29) → superseded by the per-instance message_queue_mode backlog|coalesce redesign at the message-queue layer (2026-07-05, transcript); story:most-recent-coalesces-cascades retired in favor of the queue-layer story.
- rimsky_instances.userdata_overrides → renamed attribute_overrides with the userdata→attributes collapse (2026-05-20, artifact).
- Per-instance main-RunScope model (2026-05-22) → frame-owned root RunScopes (2026-06-30, transcript).
- Persisted match-count column with synchronous supervisor increments (2026-05-21) → event-derived counts (2026-07-06, transcript).
- `rimsky run --terminate-after-run` / watch-blocks-on-terminated (2026-06-10) → watch --until idle default over pending-message + open-frame idleness (2026-07-07, transcript).
- Cross-cutting instance:true subscriptions with dual wait-set rows (2026-05-14, artifact) — tied to wait_set.subscription_scope, which was dropped in migration 016 (2026-07-07, transcript).
- The spec's await_async terminate scenario test was dropped in favor of a structurally-equivalent seeded handler test (2026-05-28, divergences, artifact) — later re-tightened by the 2026-06-06 full-stack proof requirement.

## Conflicts needing human ruling

- The 2026-07-06 transcript says a terminated instance's queue "drains without effect" while the 2026-07-13 adjudication rules terminate must actively cancel pending messages (finding 437). The adjudication is later and explicit — cancel wins — but the ledger should confirm no surface still depends on passive draining.
