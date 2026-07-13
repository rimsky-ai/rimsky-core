# Intent Dossier: control-api

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

The June 14–17 message-schema redesign reshaped the operator surface: ad-hoc node
invalidation, backfill, and asset-materialize all collapsed into (or were removed in
favor of) the messages endpoint. May-era artifact promises about those retired surfaces
are historical and must not be used to restore them.

## Net position

- The control-api is the one public operator contract. External tools consume rimsky only via the protocol contracts (executor, claim-producer, control-api), never rimsky source (2026-05-19, crimefinder, artifact). Even in all-in-one/self-host mode the CLI talks to itself through its own control-api over loopback HTTP — a single client code path so every self-hosted run proves the real API surface; only the operator surface goes through HTTP, bundled service dispatch stays in-process (2026-07-04, 3f71f90a, transcript).
- All HTTP routes live under a /v1/ prefix, including the executor async-callback URL and the observability sub-router; old bare paths were removed with no transition window (2026-06-08, corpus-bootstrap, artifact; supersedes the earlier bare-path posture).
- Auth is API-key based: rk_<44-char-base64url> tokens, SHA-256-hashed at rest in rimsky_api_keys, presented via Authorization: Bearer, with a JSONB permission grant per key (2026-05-15, control-plane-mcp-and-auth, artifact). The auth middleware runs before every handler on both protocol skins, with no exemption except the health/ready probe paths; every endpoint declares its action via the canonical action registry (2026-05-15, artifact).
- MCP is a first-class protocol skin, not a separate server: POST /mcp on the same server, port, auth middleware, and audit pipeline; a tools/call runs the same handler as the equivalent HTTP route; audit rows differ only in protocol_skin (2026-05-15, artifact). Every operator action on the HTTP surface is also an MCP tool with the same gate, state effects, and response (2026-06-02 / 2026-06-08, artifacts).
- Dry-run is a per-request ?dry_run=true flag honored by the auth middleware — the only source of dry-run; no preview-only keys; default is execute (2026-05-29, console-upstream-auth, artifact). Every write action must have a dry-run branch with no carve-outs, structurally guaranteed by a conformance test that enumerates every write action (2026-05-29, artifact).
- Message ingress is one route: POST /v1/instances/{id}/messages for operator and publisher senders alike, dispatching on the auth context ("one URL", 2026-06-19, 8a3b8c19, transcript). sender_kind is dropped from the wire envelope — auth path is the discriminator; the server stamps the persisted sender_kind (operator/publisher/instance) from auth context; 'instance' is runtime-internal and never on the wire (2026-06-19, transcript).
- Operator "invalidate this instance" is just posting a message of a template-declared type — the same path as publisher and cascade-emitted messages (2026-06-14, bfc9febb, transcript). There is no general operator verb to re-run or invalidate arbitrary nodes; direct node targeting is a permission-gated debug feature only (2026-06-15, 4c42fe5b, transcript: "re-running arbitrary nodes breaks the model").
- The debug channel is POST /instances/{id}/debug/override (actions invalidate_node, set_attribute), applied synchronously in the request transaction, legal only when the instance is paused or an unresumed pause-mode breakpoint hit blocks a runner; refused 409 outside the gate; guarded by the dedicated instance:debug-override permission not granted on standard operator keys; every use emits a debug.override.applied audit event; overrides do not persist beyond the running frame (2026-06-14/15, bfc9febb/91ec93d1, transcript).
- Instance creation is a pure two-step flow: POST /instances creates the row and nothing else; any caller wanting work issues a follow-up (empty) message (2026-06-15, 4c42fe5b, transcript).
- POST /nodes/{id}/reset survives as a pure retry-budget clear — a named carve-out of the breakpoint-only principle for a frame blocked on an error: clears the failed-terminal row's evaluator state, performs no wake and no invalidation, refuses 409 on a node that is not failed-terminal; to retry, the operator posts a message (2026-06-16/17, 4c42fe5b/b95ff4a7, transcript).
- The node read surface presents no synthesized single-value state: the response carries fields the node row owns plus the 4-bucket categorical run_summary (active = running+held+parked, pending = pending+stale, fresh, failed) and latest attributes (2026-06-21/29, 10cf843b/8a8539a4, transcript).
- Audit/event writes are durable and synchronous — never silently dropped; the per-request auth.access_attempted row is written inline in the request goroutine (2026-05-29, artifact).
- Producer errors cross the HTTP boundary intact: error class and message in the body, 422 producer-rejected-input vs 502 producer-failed vs 500 rimsky-internal (2026-06-11, last-mile-stability, artifact).
- The compose: reserved prefix on tags/instance-keys is server-enforced: control-api rejects foreign compose:-prefixed creates with 400; the real compose machinery (compose-origin marker) succeeds (2026-06-06, comprehensive-gap-closure, artifact; supersedes the client-side-only posture).
- Rimsky has no process-to-process auth (deployment-level network isolation; cataloged tension), and external IdPs are out of scope by design — rimsky's surface is the API-key floor (2026-05-15 / 2026-05-24, artifacts).
- Every new route requires coordinated edits enforced by the action registry (panics on collision): gated route registration, a v1Actions entry, an MCP tool name when exposure is intended — plus builtinSchemas lockstep, the fourth edit the discipline initially missed (2026-05-28, quality-of-life, artifact).

## Required behaviors (open promises)

Messages endpoint:
- POST /instances/{id}/messages refuses any request without an Idempotency-Key header with 400 and persists nothing; keyed first POST returns 201; same-key replay returns 200 with the identical message_id and no second envelope (2026-06-06, comprehensive-gap-closure, artifact: "replay-dedup is mandatory and a missing key can never silently bypass it"; replay semantics re-ratified in transcript 2026-06-16/17).
- A message of a type not declared in the template's messages: registry is refused loudly (HTTP 400-class naming the unknown type and the declared set); a declared type persists to the ledger and opens a frame (2026-06-14, bfc9febb, transcript).
- The empty type "" is reserved for the runtime root-wake; author-declared empty message types are rejected at template validation (2026-06-17, b95ff4a7, transcript).
- Publisher sends carry publisher_subscription_id, resolved against an active subscription scoped to that instance: 403 on unknown/cross-instance/stopped, 400 on missing; the server overwrites sender from the authoritative row so publishers cannot spoof (2026-05-17, sensor-messaging-unification, artifact).
- A per-status test matrix pins every idempotency and publisher-capability outcome to its exact HTTP status so any single-status regression fails the build (2026-06-06, artifact).

Auth / permissions / audit:
- Authenticated requests land in the event log: auth.access_attempted on every request, auth.access_denied with typed denial_reason (no_token/invalid_token/expired_token/revoked_token/permission_denied), plus key lifecycle events; the 401 body carries denial_reason (2026-05-15, artifact).
- GET /audit under the dedicated audit:read action filters auth.* rows by actor, action, result, mode, time with cursor pagination; ?target= is rejected 400 rather than silently full-scanning (2026-05-29, artifact).
- MCP tools/list is filtered by the requesting key's grant; tools/call re-evaluates the permission through the same code path as HTTP (2026-05-15, artifact).
- Auth scenario tests cover bootstrap, wildcard grants, first-match-wins, dry-run, rotation grace, revoke-last-key guard, anonymous-mode transitions, MCP-vs-HTTP parity, and audit content (2026-05-15, artifact).

MCP skin:
- Transport parity: the tool surface covers every read and mutation the HTTP surface offers; invoking a tool fires the same auth gate, produces the same state, returns the same response (2026-06-08, corpus-bootstrap, artifact; proof samples one read + one mutation per category by accepted decision).
- Tool descriptors must mirror the current wire shape — message_send advertises type (required) and not the retired kind/target fields; a stale descriptor makes every MCP-driven send fail (2026-06-15, 91ec93d1, transcript).
- Streamable-HTTP connect support: initialize returns an Mcp-Session-Id header, any id-less notification gets 202 Accepted with empty body and no JSON-RPC error, GET /mcp returns 200 with a valid (possibly idle) text/event-stream (2026-06-02, rimsky-core-remediation, artifact).

Templates / validation:
- POST /templates/validate + `rimsky template lint` run the full registration validation pipeline without persisting; validate returns 200-with-findings (lint semantic) under the read-shaped template:validate action (2026-05-28, quality-of-life, artifact).
- Static-validator advisory warnings reach both register and validate responses via validation_warnings, and warnings_as_errors=true trips on them (2026-06-11, last-mile-stability, artifact).
- An uncovered substitution ref fails registration with a structured substitution_ref_uncovered error naming receiver, ref text, property path, and a copy-pasteable suggested subscribes: entry (2026-06-14, 37e2ea5e, transcript).
- Builtin executor knowledge lives in one place: the builtin package's lookup helpers serve both control-api template-validation hooks and supervisor schema resolution (2026-06-18, 8e7e4c10, transcript).

Instances:
- POST /instances/{idOrKey}/terminate exists as the production teardown path (2026-05-28, artifact); force-terminate is the distinct instance:kill action with a would_have_terminated dry-run envelope (2026-05-28, artifact). Terminating/disabling lets the current frame finish, then rejects new messages, opens no new frames (2026-07-06, 3f71f90a, transcript).
- Pause: paused flag on create (not part of the dedup key), POST pause/resume deliberately non-idempotent (409), paused surfaced in the GET projection; the supervisor never claims paused instances (2026-05-24, instance-debugger, artifact).
- GET /instances/{id} exposes service_bindings and created_by_api_key_id (proxy cache-miss fallback reads exactly these) (2026-05-24, host-agent-and-proxy, artifact).
- After messages-as-nodes, the node listing includes materialized message-receiver-nodes alongside user-declared nodes while node_count reports the user-declared count (2026-06-22, 10cf843b, transcript).

Nodes / observability:
- run_summary 4-bucket mapping as in Net position (2026-06-21, 10cf843b, transcript).
- The latest terminal run's settling_signal_type appears on both node detail GET and the instance-nodes LIST; the CLI uses it as the failure reason (2026-06-22, 10cf843b, transcript).
- A node's most-recent resolved attribute bag is readable on GET /nodes/{id} and the observability node surface (2026-06-06, comprehensive-gap-closure, artifact).
- Dashboard read endpoints are gated behind observability:read (401/403 with no key) and reflect real seeded runtime state (2026-06-02, acceptance-coverage-recovery, artifact).
- Runtime diagnostics for a wedged instance: parked nodes (with resume reason, ?reason= validated against the enum with 400 + valid options), live wait-set edges, held frames, current claim holders (2026-05-08 / 2026-05-14 / 2026-06-08, artifacts — artifact-only as an ensemble but never retracted; the held-frames diagnostic is transcript-ratified 2026-06-17). Claim-handle holders returns 200 with {"holders": []} when empty, not 404 (2026-05-04, artifact-only).
- The health route requires no auth and is probe-fast (2026-06-08, artifact); /v1/health computes supervisor active-node counts on demand, keeping the JSON field name (2026-06-21, 21306ffe, transcript).

Breakpoints / debugger:
- Breakpoint hits are discoverable by polling: MCP resources/read over two URI families with a request-carried ?since= cursor (no server session state), and the HTTP-only GET /instances/{idOrKey}/breakpoint-hits mirror ({hits, next_since, truncated}) (2026-05-24 / 2026-05-28, artifacts).
- Hit resume is idempotent on hit_id (second resume → 200, first_resume:false, original outcome preserved); resume of a cascade-deleted hit → 404 (2026-05-24, artifact).
- The debugger stays layered: transport-neutral core, HTTP adapter, MCP adapter; the rimsky:// URI scheme parsed in exactly one file (2026-05-24, artifact).

CLI-facing (server contract):
- Named client contexts for multiple control-api endpoints (2026-06-08, artifact).
- rimsky watch --until idle|terminated (idle default: no open frame and no pending messages), backed by a ?pending=true message filter through persistence, control API, and client, with conformance coverage on both backends (2026-07-07, 3f71f90a, transcript).
- rimsky run <template> self-hosts the entire stack in-process on a loopback port when no endpoint is configured; --endpoint + --self-host is a usage error; one-shot, zero-config (2026-07-04, 3f71f90a, transcript).
- instance status and watch are client-side aggregators over existing read endpoints — no new server endpoints (2026-05-28, artifact).
- The events cursor is the server's opaque next_cursor token; clients pass it back verbatim (2026-06-02, rimsky-core-remediation, artifact).

Startup / wiring:
- ResyncPublisherSubscriptions runs at control-api startup, re-issuing dropped subscriptions for live instances and stopping orphans (2026-06-02, rimsky-core-remediation, artifact).
- The standalone control-api binary wires the publishers: config block (the three-container split must behave like all-in-one) (2026-06-02, artifact).
- compose run drives the in-process unified launcher over loopback HTTP, no auth (loopback isolation), retrying bind conflicts up to 3 times, with adaptive poll back-off (2026-06-13/14, 65667e33/f0176bde, transcript).

## Intentional absences

Absence of each item below is BY DESIGN. Do not restore.

- **Operator-invalidate routes** — POST /v1/nodes/{id}/invalidate and the admin sibling, their CLI subcommand, the node:invalidate action, and the synthetic-envelope path they used are retired entirely; an operator posts a template-declared message; ad-hoc force-stale is the debug-override endpoint only. There is no third operator-invalidate surface (2026-06-15, 91ec93d1, transcript: "retire it"; reaffirmed as intentional 2026-06-15, 4c42fe5b).
- **General re-run / arbitrary-order node invocation verb** — rejected; debug feature with permission only (2026-06-15, 4c42fe5b, transcript).
- **Asset-materialize endpoint** (POST /instances/{id}/assets/{alias}/materialize) — retired with its handler, route, CLI subcommand, MCP tool schema, and action row (verified by zero grep matches); re-materialization is expressed only through messages; asset list/detail/versions/materialization-history/delete stay (2026-06-15/17, 4c42fe5b/b95ff4a7, transcript).
- **Backfill as a named primitive** — dedicated endpoints, CLI subcommands, and the backfill_operation_id lineage column all drop; a backfill is a message whose body carries a partition override (2026-06-14, bfc9febb, transcript).
- **Dedicated wake endpoint** (POST /instances/{id}/wake) and any convenience start/auto-start flag on create — rejected as parallel non-message paths (2026-06-15/16, 4c42fe5b, transcript).
- **Synthesized single-value node state field** — deliberately retired in favor of run_summary; restoring it was explicitly rejected and tests were rewritten instead (2026-06-21/29, 10cf843b/8a8539a4, transcript).
- **Operator manipulation of the instance message queue** — deliberately left out of the message-schema spec, not even mentioned; a future session will take it up (2026-06-14, bfc9febb, transcript).
- **Programmatic cancel primitive for future-delivery messages** — declined (operator delete-by-id suffices); the timer-message feature itself was dropped from that spec's final scope (2026-06-16, 055468fc, transcript).
- **Operator force-wake of an in-flight or parked run** — no v1 surface; kill-instance is the escape hatch (2026-06-20, 8a3b8c19, transcript).
- **Events streaming (SSE on GET /events) and breakpoint.hit event-log emission** — out of v1 scope; polling is the accepted fallback (2026-06-18, 9fb55f08, transcript).
- **MCP server push** — no server-initiated notifications, no resources/subscribe, no live streaming; the v1 GET /mcp stream exists only so the default client's probe succeeds (2026-06-02, rimsky-core-remediation, artifact; also 2026-05-24 instance-debugger). Webhook/SSE breakpoint-hit delivery declined (2026-05-24, artifact).
- **Vestigial heartbeat plumbing** — active_node_count column, UpdateActiveNodeCount, livenessTick, heartbeat_interval_ms all deleted; /v1/health computes on demand (2026-06-21, 21306ffe, transcript).
- **sender_kind on the wire message envelope** — dropped; auth context is the discriminator (2026-06-19, 8a3b8c19, transcript).
- **Per-sender-kind message URLs** — rejected; one URL (2026-06-19, transcript).
- **Standalone MCP-server Go module** (mcp-servers/control-api) — deleted with no alias or compat shim; the skin lives inside control-api (2026-05-15, control-plane-mcp-and-auth, artifact).
- **External IdP integration** (OIDC, SAML, JWT validation, mTLS termination) — out of scope by design, not deferred (2026-05-15, artifact). Also deferred-by-decision for auth V1: rate limits, confirmation gates, action+resource scoping, server-side roles, MCP resources/prompts beyond the debugger surface, MCP stdio transport (2026-05-15, artifact-only).
- **"External invalidate with payload" back door** — never existed; X-as-executor idiom or (historically) the admin invalidate escape hatch, itself since retired (2026-05-08, artifact).
- **Bare (un-prefixed) route paths** — removed by the /v1/ sweep with no transition window (2026-06-08, artifact).
- **Preview-only keys / dry-run key modes** — the per-request flag is the only source of dry-run (2026-05-29, artifact).

## Corrections and restorations (drift-fight record)

- **Synthesized node state re-drift.** After single-value node state was removed by design, the API was found presenting a state synthesized from the latest run — "thought we explicitly removed this"; removed again, consumers moved to run_summary (2026-06-21, 10cf843b, transcript). Precedent: retired read-surface abstractions get re-excised, not accommodated.
- **Operator-invalidate "necessitated" reversal.** A review cycle decided to keep the operator-invalidate route as necessitated; the user overruled and retired it entirely (2026-06-15, 91ec93d1, transcript). Precedent: reviewer accommodation does not outrank the design ruling.
- **Publisher resync dead code.** ResyncPublisherSubscriptions was documented as running at startup with zero call sites; wired at control-api startup and every stale "supervisor startup" doc claim corrected (2026-06-02, artifact).
- **Split-deployment publisher wiring drift.** The standalone control-api binary never wired publishers:, so every subscription failed with unknown_publisher in three-container splits while all-in-one worked — two parallel construction paths drifted; closed in the binary (2026-06-02, artifact).
- **Audit silent drop.** The async best-effort audit writer dropped rows under load, contradicting the forensic-record framing; replaced with synchronous inline writes (2026-05-29, artifact).
- **Quiescent-instance message stranding.** A message POSTed with no running frame persisted but never delivered; fixed in the same transaction (2026-06-02, artifact; the delivery mechanism was later redesigned, but the never-strand posture stands).
- **Warnings never connected.** validation_warnings, the warnings, and warnings_as_errors all existed but were never wired; connected on both register and validate (2026-06-11, artifact).
- **watch cursor bug.** The CLI fabricated a numeric seq cursor against the server's opaque token, 500ing on first advance; also the plan's own single-watermark dedup guard was unsafe and corrected during implementation (2026-06-02, artifact).
- **Invalidate-conflict rule mis-stated.** Code/doc text claimed invalidate valid only for parked/fresh; actually rejected only for running — text and pinning test corrected (2026-06-02, artifact; the route is since retired, but the fix-the-sentinel-and-test pattern is precedent).
- **GET /instances/{id} missing proxy fields.** service_bindings and created_by_api_key_id were absent though the spec's cache-miss fallback read exactly them; added (2026-05-24, artifact).
- **MCP schema gaps (standing defects at ship time).** builtinSchemas returned an empty map after the legacy tools.go was deleted pre-copy — every tool advertised the generic object schema (2026-05-15, artifact); template_validate and instance_kill later landed in v1Actions but not builtinSchemas, with no lockstep test (2026-05-28, artifact). The lockstep expectation is on record; the gap was known-open at the artifact boundary.
- **Missing planned guards (known-open at artifact boundary):** TestRegistryCoversRouter (router-walk guard for the auth-before-every-handler invariant) never implemented (2026-05-15, artifact); the MCP conformance-probe --mode=auth-mcp never implemented (2026-05-15, artifact); the 2026-05-17 idempotency/capability unit tests were never added and a header comment falsely claimed they existed — substantially restored by the 2026-06-06 per-status matrix promise.
- **Untested-endpoint backfill.** Asset (4 of 6) and lineage (5 of 8) handlers were wired untested; backfilled, and the whole-system feature-trace pass made a standing check (2026-06-02, artifact).

## Superseded / historical

- Bare paths, no API versioning, /v1/ only on observability (2026-05-04 artifact) → full /v1/ sweep of every route (2026-06-08 artifact).
- compose: prefix reservation enforced client-side only (2026-05-04 artifact) → server-enforced 400 on foreign clients (2026-06-06 artifact).
- GET /dispatches → GET /node-runs with node_runs body key (2026-05-12 artifact).
- Standalone strict-pass-through MCP shim module (2026-05-08 / 2026-05-11 artifacts) → in-server MCP protocol skin, same handlers and auth (2026-05-15 artifact); the module-layout doc's separate-module claim corrected 2026-05-24.
- Generic admin node-invalidate endpoint keyed on node state (2026-05-08 artifact), per-emit --frame flag on admin invalidate (2026-05-05 artifact), and the 2026-06-06 frame:in join-the-running-frame fix promise → all retired with the operator-invalidate surface (2026-06-15 transcript).
- Unified message layer with kind/sender/sender_kind/target envelope and single invalidate kind (2026-05-15 artifact) → template-declared typed messages; kind/target retired; sender_kind off the wire (2026-06-14..19 transcript).
- POST /sensors/{watch_id}/observations dedicated route (pre-2026-05-17) → deleted; publishers use the generic messages endpoint (2026-05-17 artifact).
- Optional Idempotency-Key with 24h-TTL dedup (2026-05-17 artifact) → header mandatory, 400 without it (2026-06-06 artifact).
- Backfill create/list/show/partitions/cancel endpoints and CLI (2026-05-15 artifact) → dropped with the backfill primitive (2026-06-14 transcript).
- Asset versions endpoint shipped as a 501 stub and the {node_type}.{producer_name} alias approximation (2026-05-15 artifact) — recorded ship-time gaps on a surface later reshaped by the materialize retirement; the remaining asset read/delete surfaces stand.
- Signal-based park wake via the generic invalidate endpoint (2026-05-08 artifact) → park wake is executor/snooze-driven only; no operator wake surface (2026-06-15..20 transcript).
- debug-override action working name instance:debug:override → instance:debug-override, two-segment grammar (2026-06-15, 91ec93d1, transcript).
- Four-state debug gate (stuck/parked/paused/breakpoint) → paused + pause-mode breakpoint only (2026-06-14, bfc9febb, transcript).
- watch blocking on automatic instance termination → --until idle|terminated after auto-terminate was scrubbed (2026-07-07, 3f71f90a, transcript).
- Lifecycle events all fire from control-api (pre-2026-05-24 invariant) → relaxed: events fire from the process owning the state transition's post-commit fan-out (control-api for template/instance events; supervisor for sub-graph/fan-out run-scope terminals) (2026-05-24 artifact).
- MCP initialize protocolVersion 2024-11-05 (plan) → 2025-06-18 as landed (2026-05-15 artifact); spec's per-handler validate/execute factoring → shared WriteDryRunResponse early-exit helper (2026-05-15 artifact); spec's 250ms compose-run poll cadence → adaptive back-off (2026-06-14 transcript).
- "No /frames operator route needed" (2026-05-12 artifact) — a pass-scoping decline, not a standing prohibition; the observability backplane serves frame views (2026-05-11 artifact).

## Conflicts needing human ruling

- **Main-run-scope close at instance termination.** The 2026-05-24 host-agent artifacts specify control-api closing the instance's main RunScope and firing OnRunScopeTerminal before OnInstanceTerminated — but the per-instance main RunScope was abolished on 2026-06-30 (RunScope is frame-rooted; see the frame dossier). What control-api's terminate path now closes/fires for run-scope-terminal consumers (e.g. the host-agent proxy's reaping) is not restated anywhere in this record.
- **Diagnostics surface currency.** The held-frames/parked-nodes/wait-sets diagnostics endpoints are artifact-era promises under the pre-redesign frame model. The transcript re-ratifies a parked-scoped held-frames diagnostic (2026-06-17) but never re-confirms the wait-sets endpoint or the exact /admin/diagnostics/* route shapes after the /v1/ sweep and the wait-set narrowing to within-frame edges. Adjudicators should require the capability (wedged-instance diagnosis) but not a specific May-era route shape without checking.
- **Nil-permissive route gate.** The deliberate test-only bypass (nil AuthState runs handlers ungated) is on record as intended-unreachable in production (2026-05-15, artifact-only); no later entry either ratifies or retires it, and it stands in tension with the no-exemption auth invariant it rides under. Whether it should be replaced with a mechanical guard (the never-implemented TestRegistryCoversRouter) is unresolved.
