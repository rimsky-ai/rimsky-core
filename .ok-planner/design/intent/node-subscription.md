# Intent Dossier: node-subscription

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- A node's `subscribes:` block is the ONLY declaration of reactive coupling, declared on the impactee (receiver) side. Send-side invalidation targets, dependencies:, on_event: maps, and hard_dep are all retired (2026-05-14 artifact onward; hard_dep retirement 2026-06-14, 37e2ea5e, transcript).
- An entry names a canonical signal type-path (`type:` — exact match or trailing-* prefix only), an optional CEL `when:` predicate over the signal payload, an optional per-sender `node:` filter, and a required `force_upstream_refresh` boolean (2026-05-23 artifact grammar; two-flag model 2026-06-14, 37e2ea5e, transcript; wake_on_change since removed — see Intentional absences).
- Subscriptions always wake: a subscription declaration is a wake declaration. `force_upstream_refresh` is the only cascade flag of its kind (2026-06-23, 10cf843b, transcript).
- There is NO implicit/auto-subscribe. A substitution read is itself the edge; every substitution ref (nodes.* and messages.*) must be covered by an explicitly declared subscription, checked at registration; a template with an uncovered ref fails validation with a structured `substitution_ref_uncovered` error carrying a copy-pasteable suggested entry (2026-06-14, 37e2ea5e, transcript; issue #18 closed by v0.10.0 commit 19770b30, 2026-06-24, 3b1066c7, transcript).
- Coverage is asymmetric on purpose: a wildcard attribute/* subscription covers per-field reads, but a per-field attribute/Y/changed subscription does NOT cover a whole-pull read (2026-06-14, 37e2ea5e, transcript).
- Semantics are invalidate-then-pull, not push: the receiver is invalidated/stale-marked and rescheduled, then pulls the latest persisted values; nothing rides the cascade edge; subscribers receive the wake, not the payload (2026-05-29 artifact correction; restated durably when the event-vocabulary-implies-delivery tension was resolved, 2026-07-07, 3f71f90a, transcript). concept:node-subscription must describe stale-marking and wait-set gating, not delivery.
- Substitution dependencies are built at substitution time by reading each subscribed upstream sender's full persisted attribute bag (most recent fresh-settled run) via one unified BuildAttributeDeps; wait-set rows do only one job — signal which receiver to evaluate — and carry no data (2026-06-23, 10cf843b, transcript).
- Subscribable signal set: terminal/success and terminal/error/* (park subscriptions rejected at registration; event/<name> and message/* type-paths retired), plus attribute/<key>/changed for genuine diffs; tag-conditional firing is a terminal/* subscription with CEL `"<tag>" in payload.tags` (2026-06-17, b31002b8; 2026-06-24, 8a8539a4, transcript).
- Wildcards are trailing-* only — no positional wildcards, no glob; richer matching goes through CEL (2026-05-23, artifact).
- CEL (cel-go) is the filter language — a deliberate heavy-dependency exception; all when: expressions parse at registration; exact-type subscriptions get field diagnostics (implemented as an AST walk over payload.<field> selects against the type's payload schema); trailing-* prefixes bind payload as dyn with no field warnings (2026-05-23, artifact).
- force_upstream_refresh: receiver invalidation proactively pulls the named sender into the frame to re-run first so the receiver's substitution context sees fresh values; enforced across all cascade walk paths, and a direct admin-invalidate of a receiver also pulls its force_upstream_refresh upstreams (commit cc0a5aa, 2026-06-15, b106a350, transcript).
- Messages: an incoming message is a virtual node whose name is the message type; nodes subscribe to message types through the normal subscribes: block; message delivery routes purely by message_type matched against node-subscription edges (2026-06-14, bfc9febb, transcript; target_node routing removed 2026-06-19, a02fe167, transcript). Message-receiver nodes cannot subscribe and cannot substitute in — their attributes come exclusively from delivered bodies (2026-06-22, 10cf843b, transcript). The empty-message wake remains an edge-level mechanism (sender ""), not a materialized node (2026-06-22, 10cf843b, transcript).
- Subscription edges are precomputed at template registration into an in-memory per-sender prefix-keyed inverse-edge map (a derived view — not persisted, not part of the canonical template hash); structural-root edges are injected there under sender "" (2026-05-14 + 2026-05-23 artifact; injection model 2026-06-16, 4c42fe5b, transcript).
- Self-subscription is first-class (drain-my-own-queue / loop idioms); self-edges bypass the cross-terminal once-per-frame dispatch guard (2026-05-23 artifact; exercised by loop_counter and the claude-agent session-resume loop, 2026-06-17 b31002b8 / 2026-06-30 8a8539a4, transcript).
- Node execution is orderly and stepwise: a sender fully settles before any subscriber sees its signals or dispatches; event-subscription cardinality is one dispatch per frame, latest-only (2026-06-16, 055468fc, transcript; 2026-05-29 artifact).
- Subscription filters operate on signal type-paths and payload metadata via CEL at walk time — the receiver never gets payload bytes delivered (2026-05-15 inertness framing, artifact, as refined by the walk-time-CEL model).
- The names "subscribes" and "payload" are settled vocabulary — the rename to pull vocabulary (watch/body) was explicitly declined (2026-07-07, 3f71f90a, transcript).

## Required behaviors (open promises)

- Registration-time coverage check: every substitution ref must be matched by a covering declared subscription; failure is a structured substitution_ref_uncovered error with a suggested subscribes entry (2026-06-14, 37e2ea5e; confirmed closed-as-shipped 2026-06-24, 3b1066c7, transcript): "if an edge exists, the template author has to define the behavior, or the template fails validation."
- messages.<type> refs are validated identically to nodes.<type> against the combined messages-and-nodes set, with NO auto-subscribe injection (2026-06-18, 9fb55f08, transcript). The leftover messages.* auto-subscribe edge-injection code is adjudicated drift, fix-code (finding 1312) (2026-07-13, 3f71f90a, transcript).
- force_upstream_refresh honored on all cascade walk paths (terminal-driven, message-delivery, pure-cascade) and on direct admin-invalidate (walkCascadeForInvalidatedNode), pinned by a dedicated regression test (2026-06-15, b106a350, transcript).
- Registration rejects subscriptions to park signal types; docs must not claim subscribe-to-any-event (2026-06-24, 8a8539a4, transcript): "report an error if templates subscribe nodes to park."
- Trailing-*-only wildcard validation: positional wildcards and glob syntax reject at registration (2026-05-23, signal-taxonomy-and-policy-decoupling, artifact).
- CEL when: registration behavior: parse-reject invalid expressions; exact-type field diagnostics; dyn binding for prefixes (2026-05-23, artifact).
- Two-gate tag validation: registration warning for CEL tag literals not in the executor's declared_tags; runtime rejection of undeclared tags on outcomes as executor_protocol_violation (2026-06-17, b31002b8, transcript).
- Per-sender terminal/error/* subscriptions fire whether the sender settles via error_types: give_up or pass (issue #15 pin; signal-blind proof) (2026-06-10, cascade-and-claim-handoff, artifact).
- Once-per-frame dispatch guard across a terminal's multiple signals, with self-edges intentionally bypassing the cross-terminal guard so drain-my-own-queue keeps working (2026-05-23 divergences, artifact) `(artifact-only)`.
- Pass-through propagation: in A→B→C where B substitutes A's value through (including B as fan-out parent), C's attribute-changed subscription fires when the passed-through value actually changes (2026-06-22, 10cf843b, transcript).
- BuildAttributeDeps as the single substitution-context builder reading full persisted upstream bags, serving both gate-eval and acquisition call sites (2026-06-23, 10cf843b, transcript).
- Message delivery routes by message_type against subscription edges only; message-receiver nodes have no subscribes block and no source directives (2026-06-19 a02fe167; 2026-06-22 10cf843b, transcript).
- Coverage asymmetry (wildcard covers per-field; per-field does not cover whole-pull) enforced at registration (2026-06-14, 37e2ea5e, transcript).
- Self-subscription loop capability: a self-edge terminal/success subscription with a CEL payload predicate produces serially-settled dispatches within one frame/RunScope (loop_counter with loop/done tags; claude-agent session-resume via payload.attributes_delta predicate) (2026-06-17 b31002b8; 2026-06-30 8a8539a4, transcript).
- Migration values for pre-existing templates under the flag model: today-equivalent defaults were stamped explicitly (legacy hard_dep entries got force_upstream_refresh: true) (2026-06-15, b106a350, transcript) — historical migration, but the no-silent-defaults principle stands: cascade behavior is read at the call site, not inferred from documentation (2026-06-14, 37e2ea5e, transcript).
- A sensor-driven end-to-end reactive path: external change → persisted message row → subscribing downstream node goes stale → re-runs to fresh through the cascade (2026-06-02, acceptance-coverage-recovery, artifact) `(artifact-only, shapes predate the messages-as-virtual-nodes rework — the capability claim, not the row shape, is the promise)`.

## Intentional absences

- `dependencies:` as a node-template construct — decomposed into substitution refs, explicit subscribes:, and the wait-set ledger (2026-05-14, subscription-cascade-and-quality-of-life, artifact).
- Send-side `invalidate.targets` (lifecycle-handler family and error_types action: invalidate), and self-invalidate `targets: [self]` — subscription-only is canonical (2026-05-14, artifact).
- The `on_event:` handler map and the emitter-side resolve verdict — retired without replacement (2026-05-14 / 2026-05-19, artifact).
- Implicit auto-subscribe from substitution refs (including the 2026-05-20 store-selector/lock-name parser extension) — dropped for explicit coverage; the attribute concept's Non-goals entry rejecting pull_only-style flags was explicitly retired with it (2026-06-14, 37e2ea5e, transcript): "yes, retire the non-goals."
- The per-field `hard_dep: true` attribute flag — replaced entirely by the subscription-block configuration (2026-06-14, 37e2ea5e, transcript).
- The `wake_on_change` subscription flag — removed entirely, "like it never existed": struct field, validator requirement, walker branch, tests, examples, docs; the explicit-attribute-context-read story retired with it. Rationale: with substitution reading persisted stores directly, wake_on_change: false did literally nothing (2026-06-23, 10cf843b, transcript; confirmed expected-state 2026-06-24, 7e7b5913). Docs still naming wake_on_change as a required flag are drift (2026-07-11, 3f71f90a, transcript).
- `instance: true` cross-cutting subscriptions — judged an antipattern with zero real uses and completely removed (Instance field, SenderBoundToEmpty flag and filter apparatus, SubscriptionScope column via migration 016, validator branches, all concept/TD traces; git history only). Commit c6907c29 (2026-07-05, 3f71f90a, transcript). This also moots the earlier instance:true+force_upstream_refresh rejection rule and the two-wait-set-rows-per-scope behavior.
- The `frame: in|next` subscription modifier — frames are opened only by message arrival; frame: next is not restored (2026-06-15, 4c42fe5b, transcript).
- SubscriptionEntry's structured filter fields (When/Outcome/ErrorClass/Reason/Name/Kind/Sender/SenderKind/Target), including the message topic kind's kind/sender/sender_kind/target filters and the when: parked reason: filter — retired for the type-path + CEL grammar (2026-05-23, artifact). The 2026-05-19 cascade-filter-evaluation sketch is entirely superseded.
- `event/<name>` subscription type-paths and {{nodes.X.event.Y}} substitution — retired with named events; replacement is terminal/* + CEL tag filters (2026-06-16..17, 055468fc / b31002b8, transcript).
- `{{trigger.message.payload.*}}` substitution and the TriggerMessagePayload/TriggerMessageType plumbing — "there is no such thing as trigger.message"; messages are nodes (2026-06-18, 9fb55f08, transcript).
- `target_node` on publisher subscriptions — dead routing, removed end-to-end (proto, YAML, validator, column via migration 014, sensors, conformance, concepts) (2026-06-19, a02fe167, transcript).
- `target: self` message-subscription mechanics (both the validator self_alias plan and the runtime /self suffix check) — superseded by the messages-as-virtual-nodes model where receivers subscribe to the message type directly (2026-06-14 bfc9febb onward, transcript).
- Multi-source attribute substitution (per-field source arrays, first-non-missing) — declined with no code changes; per-field source arity stays 1, and the arity asymmetry with subscriptions is intentional and load-bearing (2026-05-20, multi-source-substitution-decline, artifact).
- The emit_named_event MCP tool and AsyncCallbackBody.events — retired with named events (2026-06-16, 055468fc, transcript; supersedes the 2026-05-28 artifact promise).
- Renaming subscribes/payload to pull vocabulary — explicitly rejected at tension resolution (2026-07-07, 3f71f90a, transcript).
- `lifecycle/*` node-subscription surface — declined; belongs to the lifecycle-subscriber protocol (2026-05-23, artifact).

## Corrections and restorations (drift-fight record)

- "Subscriptions remain push" doc claim → corrected to invalidate-then-pull; nothing rides the cascade edge; named events are not fan-out (once per frame, latest-only). The pub-sub vocabulary mismatch had already misled an agent into a wrong design (2026-05-29, console-upstream-auth-audit-and-fixes, artifact).
- Issue #15: per-sender terminal/error subscription silently skipped — code drift, fixed and pinned by the signal-blind proof (2026-06-10, artifact).
- Issue #18: consumers needed upstream reads without ungated cascade — resolved not by patching auto-subscribe but by retiring it for explicit coverage (2026-06-14, 37e2ea5e; closed 2026-06-24, 3b1066c7, transcript). Precedent: real consumer evidence can retire a recorded Non-goal.
- trigger.message residue (substitution path, ResolveContext fields, triggerMessageForFrame, concept:fan-out invariant) contradicted the messaging consolidation — excised (2026-06-18, 9fb55f08, transcript). Precedent: surviving code/doc surfaces of a consolidated-away mechanism are the drifted side.
- wake_on_change leftovers after removal: user audited ("we thought wake_on_change: false was removed. was it?"); only historical references may survive (release notes, retired decision doc) (2026-06-24, 7e7b5913, transcript). Docs naming it as a current required flag with a rejection invariant are drift that would mislead a maintainer into re-adding deleted behavior (2026-07-11, 3f71f90a, transcript).
- messages.* auto-subscribe edge injection surviving the June-18 coverage-check unification — adjudicated: the injection code is the drifted side, remove it (fix-code, finding 1312) (2026-07-13, 3f71f90a, transcript).
- Bare-attribute auto-subscribe pattern drift (attribute/* vs canonical attribute/*/changed) — flagged likely-accidental (2026-05-23 divergences, artifact); the mechanism is now retired but the match-the-documented-canonical-shape precedent stands.
- The legacy emit-on-pass capability gap under the fresh_changed gate (skipped scenario test, missing always_propagate) (2026-05-14 notes, artifact) — dissolved by the later signal-blind model in which pass settlements DO cascade via terminal/error; adjudicators should not treat the old skip as current intent.

## Superseded / historical

- 2026-05-14 grammar (node:/instance:, on: state|attribute|event topic kinds, structured filters, frame: in|next, declared-events registration check) → 2026-05-23 type-path + CEL grammar → 2026-06-14 two required booleans → 2026-06-23 wake_on_change removed → 2026-07-05 instance:true removed. Current grammar: type + when + node + force_upstream_refresh.
- Wait-set drain-to-eligibility model with dual rows per subscription scope (2026-05-14, artifact) → subscription_scope dropped (migration 016) with cross-cutting removal.
- Auto-subscribe rule carried forward under the taxonomy (2026-05-23, artifact) → dropped entirely 2026-06-14.
- "Subscription topic filters operate on metadata, never payload bytes" (2026-05-15, artifact) → refined: CEL when: predicates DO evaluate payload fields, but only at walk time inside the engine; payloads are still never delivered to receivers.
- SenderBoundToEmpty discrimination of cross-cutting vs structural-root edges under the "" sender key (2026-06-16, 4c42fe5b, transcript) → apparatus removed with cross-cutting subscriptions (2026-07-05); the empty-message wake path (emptyMessageWake*) remains, keyed by sender "".
- The wake_on_change: false context-gathering-edge semantics (unconditional wait-set insert, no stale-mark) (2026-06-14, 37e2ea5e) → moot after wait-set rows stopped carrying data and the flag was removed (2026-06-23).
- Concept rename: receiver-side "subscription" → "node-subscription", orthogonal to publisher-subscription (2026-05-17, sensor-messaging-unification, artifact) — still the naming in force.
- Named-event consumption rules (ledger reads, {{nodes.X.event.NAME.path}}, on:event) (2026-05-19..28, artifact) → retired wholesale with named events.

## Conflicts needing human ruling

- None recorded beyond those noted in the signal dossier (subscribability of transient/retry/*, which equally affects what the subscription validator should accept).
