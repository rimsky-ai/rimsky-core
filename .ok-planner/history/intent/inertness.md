# Intent Dossier: inertness

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Inertness (renamed from "opacity", 2026-05-12, nomenclature-resolution, artifact) has two sub-disciplines that must not be conflated: byte-opaque inertness — rimsky never traverses (claim scope/address/payload, blob bytes, park/snooze payload, executor scratch); and structural inertness — rimsky may traverse for transport/substitution mechanics but never interprets values (attribute values, named-event payloads, message payloads) (2026-05-12; 2026-05-19, crimefinder, artifact).
- The sanctioned read sites are precisely enumerated: (1) the substitution engine's walkPath leaf extraction (lazy-unmarshal into a transient map at extraction time only); (2) the blob-spill mechanism moving bytes between the inline column and the backend; (3) the matcher evaluator's primitive-equality read of attrs.<path> predicates (2026-05-04, modeling-layer-contract; 2026-05-08, platform-extensions; 2026-05-21, attribute-overrides-matcher-overlay, artifact).
- The uniform prohibition: inert bytes are never logged, %v-formatted, validated beyond schema gates, transformed, attached to traces, or included in error messages (2026-05-11, log-convergence; 2026-05-17, post-data-platform-cleanup, artifact).
- The same leveling discipline applies to executor-specific vocabulary: anything executor-specific lives behind the executor protocol; rimsky core, the scheduler, and the persistence layer know nothing about it (2026-06-14, 752fe200, transcript).

## Required behaviors (open promises)

- Claim content (address, payload, scope) is inert in foundation: read only by the modeling layer at substitution leaf-extraction time via walkPath; no other code path reads it; every rimsky-side helper (e.g. abandonOpenedClaim) passes scope/address through opaque (2026-05-04, foundation-contract / modeling-layer-contract; 2026-05-11, log-convergence, artifact).
- Named-event / event payloads: append-only ledger, payloads spillable via the blob backend, bytes read only via walkPath substitution and the persistence fetch on consumption; most-recent emission of (emitter, event_name) wins at substitution time (2026-05-11, log-convergence, artifact). The emit_named_event tool passes payload through opaquely (2026-05-28, quality-of-life-features, artifact-only).
- Message payload bytes are inert: read only via {{trigger.message.payload.<path>}} walkPath substitution at dispatch time and the GET /messages/{id} fetch (2026-05-15, data-platform-extensions; 2026-05-17, sensor-messaging-unification, artifact). Message bodies are NOT validated at receipt — body_schema serves registration-time substitution-ref checking; actual validation happens only when a receiver pulls values at dispatch through the attribute-validation gate (2026-06-14, bfc9febb, transcript).
- Blob bytes are inert (the second named exception): rimsky reads attribute bytes only to walk substitution paths and to move bytes between column and backend; audit entries for park events record payload size and a spill flag, never bytes (2026-05-08, platform-extensions, artifact).
- Attribute-value structural inertness: override and default values are static — no substitution applied; an operator-supplied "{{X}}" is a literal; override/default validation inspects only routing keys (by_executor/by_node and the names they reference), never fragment values (2026-05-20, userdata-collapse-into-attributes; 2026-05-19, multi-instance-template-ergonomics, artifact).
- The matcher evaluator's sanctioned read is primitive-equality only, no traversal beyond the named path, values never logged/formatted/in errors (2026-05-21, attribute-overrides-matcher-overlay, artifact).
- Subscription topic filters operate only on metadata (state, attribute key, event name, error class), never payload bytes (2026-05-14, subscription-cascade-and-quality-of-life, artifact-only).
- fan_out partition_request is opaque bytes passed verbatim to SplitScope after {{...}} rendering; rimsky never parses it (2026-05-19, crimefinder, artifact-only).
- producer_candidate_handle bytes live on sub-claim rows, stored and passed verbatim to the producer, never inspected; they reach the leaf executor inside the per-claim address structure (2026-05-15, data-platform-extensions, artifact-only).
- Park's reason_label is a free-form string opaque to rimsky (read-only persistence, never used for routing); the park/snooze payload is bytes handed back verbatim on resume — deliberately typed differently from Error.payload (a Struct traversable for audit transport) (2026-05-12, nomenclature-resolution; 2026-05-22, fan-out-safety-scope-first, artifact).
- Executor scratch: opaque bytes attached to a terminal event, persisted on the node_run, handed back verbatim on the next dispatch's ExecuteRequest across recovery — copied across all three prior-dispatch dispositions (heartbeat-stale, retry-after-error, recalculate) because recovery retires the old row; the mid-dispatch write is an HTTP callback route on the executor protocol (2026-06-14, 752fe200, transcript).
- The breakpoint-hit snapshot keeps claim content opaque even at the debugger: held_claims and open_wait_set are summaries (IDs, types, counts); merged_attributes is full because the matcher grammar already sanctions attribute-value visibility (2026-05-24, instance-debugger, artifact).
- The bundled claude-agent executor adds nothing beyond the protocol — no bypass of payload inertness, no sibling-node attribute reads (2026-06-19, 8e7e4c10, transcript).
- Audit records store request_params verbatim (not hashed) — justified because control-plane request bodies never carry secrets; the API key travels in the Authorization header and is never stored (2026-05-15, control-plane-mcp-and-auth, artifact-only).
- The Validation protocol forwards opaque bytes and receives a verdict — the method is plain Validate, and rimsky never inspects the forwarded config/userdata content (2026-05-15, data-platform-extensions, artifact-only).

## Intentional absences

- Blessed invariant 11 ("userdata is inert") and the userdata concept — retired with the userdata-into-attributes collapse; attribute-value inertness is covered by the general structural-inertness discipline, and the userdata-schema-as-opacity-exception tension dissolved with it (2026-05-20, userdata-collapse-into-attributes, artifact).
- payload_template pre-shaping on publisher observations (server-side substitution at deposit time) — dropped; publishers send raw bytes, receivers substitute at dispatch (2026-05-17, sensor-messaging-unification, artifact).
- Named session_token fields on Park / ResumeContext in the core protocol — mis-leveled (a Claude-Code-CLI concept in rimsky's protocol/runtime/persistence); session-resume vocabulary must be executor-specific behind the protocol (2026-06-14, 752fe200, transcript).
- Executor-named persistence surfaces (e.g. a rimsky_pending_timer table) — the persistence layer must not know executor specifics; any executor state surface is generic and exposed through the protocol (2026-06-14, 752fe200, transcript).

## Corrections and restorations (drift-fight record)

- A runner.go comment claimed userdata validation was "post-substitution" — userdata is never substituted; corrected during review-cleanup (2026-05-08, platform-extensions, artifact).
- Park.session_token / ResumeContext.session_token as named core-protocol fields — ruled a leveling violation and superseded; the scheduler must know nothing about resuming (2026-06-14, 752fe200, transcript).
- The scratch wire surface went through review correction: "scratch survives via in-place row transition" and "scratch_set as universal MCP tool" framings were replaced by explicit copy-across-dispositions and an HTTP callback route (2026-06-14, 752fe200, transcript).

## Superseded / historical

- Concept name "opacity" → "inertness", and invariant wording "userdata is opaque to rimsky" → "userdata is inert in Rimsky" (2026-05-12, nomenclature-resolution, artifact).
- Userdata as a distinct inert surface with its own invariant → absorbed into attributes under structural inertness (2026-05-20).
- Historical numbered labels (invariants 11, 20, 21, 24) — the numbering scheme itself was later banned from source and diagnostics (2026-06-19, a02fe167, transcript, recorded on the conformance dossier); the disciplines the numbers named remain in force under descriptive names.

## Conflicts needing human ruling

None recorded — the precedence rules resolve the record's tensions on this concept.
