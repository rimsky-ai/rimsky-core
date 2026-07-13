# Intent Dossier: event-log

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- There is one event log: `rimsky_events`, rimsky's own append-only audit ledger with rimsky-readable JSONB payloads, written by supervisor/scheduler/control-api at observable transitions and served at GET /events. The second table of the 2026-05-11 split — the named-event ledger `rimsky_node_events` — is gone (dropped in migration 013 with the whole NamedEvent mechanism, 2026-06-17, b31002b8, transcript).
- Kinds are typed at the app boundary: signal-class kinds keep the signal type-path discipline; operational kinds are a proto `OperationalKind` enum in events.proto; app logic consumes typed values exclusively, never raw strings; the column stays TEXT with no CHECK — the enum at the app boundary IS the gate; an unknown string at unmarshal is a defensive error (2026-06-08, corpus-bootstrap, artifact).
- Payloads have two shapes: typed oneof payloads for signal-class events, free-form JSON for operational events whose payload is audit data (2026-06-08, artifact).
- Every signal emit — cascading or not — writes exactly one audit row with kind = signal type-path, independent of subscriber match; an operator's event-log query and a subscriber's wait-set see the same signal (2026-05-23 + 2026-06-10, artifacts).
- Audit/event writes are durable, synchronous, and never silently dropped; the per-request auth audit row is written inline in the request goroutine under a background context so client disconnects cannot abort it (2026-05-29, console-upstream-auth-audit-and-fixes, artifact).
- Retention: event logs are time-keyed (rimsky_events by occurred_at) and reaped under the shared trailing trace-retention window alongside frames and node_runs; within the window the log is append-only; in-flight frames are never reaped (2026-06-03, instance-lifecycle-durable-by-default, artifact — this explicitly replaced the older "no built-in retention" invariant).
- Vocabulary: node_runs *emit* events and *send* messages; nodes do not emit messages (2026-07-05, 3f71f90a, transcript, user). Events are internal-to-rimsky and frame-synchronous, distinct from messages (2026-05-15, artifact).
- Frame processing writes only to node_runs and the event log, never to the instance row (2026-07-06, 3f71f90a, transcript).

## Required behaviors (open promises)

- Auth auditing: every authenticated request lands `auth.access_attempted` (dry-runs included, `executed:false` where applicable); 401/403 lands `auth.access_denied` with typed denial_reason (no_token, invalid_token, expired_token, revoked_token, permission_denied); key lifecycle lands `auth.key_created` / `key_revoked` / `key_rotated` — all on rimsky_events with structured JSONB payloads (2026-05-15, control-plane-mcp-and-auth, artifact-only).
- `GET /audit`, gated by the separately-grantable `audit:read` action, filters the auth.* rows by actor, action, result, mode, and time with cursor pagination and expression indexes; no dedicated audit table; exposes all five auth.* kinds (2026-05-29, console-upstream-auth-audit-and-fixes, artifact-only; restated 2026-06-08, corpus-bootstrap). `?target=` is rejected with 400 rather than silently full-scanning — reject, don't silently degrade (2026-05-29, artifact-only).
- Audit records store request_params verbatim, not hashed; the API key travels in the Authorization header and is never stored in audit records (2026-05-15, artifact-only).
- `?dry_run=true` on a read is honored as a no-op preview: the read runs and the audit row records mode dry_run with executed:true (2026-05-29, artifact-only).
- Every `work_started` pairs with a `work_completed` appended at terminal application, same identifying fields plus terminal kind, parked / await-async re-entry excluded, so durations and did-everything-finish audits are ledger-computable (2026-06-11, last-mile-stability, artifact). Park deliberately emits no work_completed — park is in-flight; the run's eventual terminal emits the pairing event (2026-07-13, 3f71f90a, transcript; adjudicated fix-doc, finding 2348).
- Attribute-override match counting is event-borne: each match emits an `attribute_override_matched` operational event at dispatch time; the API aggregates from the event log at read time (2026-07-06, 3f71f90a, transcript, user: "option a").
- Attribute-changed audit emission is driven by the same persisted-delta diff that drives the cascade, uniform across every settle path, so the events log matches what the cascade fired (2026-06-22, 10cf843b, transcript, user).
- The debug-override endpoint records a `debug.override.applied` audit event whose mutated counter counts only rows actually modified, never attempts (2026-06-14/15, bfc9febb / 91ec93d1, transcripts).
- TransitionReason remains the closed runtime state-machine validation enum (NextState), covering non-signal transitions (dispatch_claimed, pure_cascade, infra_reenqueue — notably the dispatch_claimed rejection guarding double-execute); only its audit-write role retired for signal-bearing transitions; non-signal transitions still write TransitionReason kinds (2026-05-23, signal-taxonomy-and-policy-decoupling, artifact). `instance_killed` is validation-only, never an audit kind; the teardown's auditable cause is one administrative `instance_terminated` row (2026-05-28, quality-of-life-features, artifact-only).
- Non-cascading signals (park, infra, await_async) still land bare audit rows (2026-06-10, cascade-and-claim-handoff, artifact). The dual terminal-signal emission — once inline into the cascade walker, once post-commit as the canonical audit write — is deliberate, not a bug (2026-05-23, divergence audit, artifact-only).
- Downstream consumers must not assume every dispatch produces a terminal event: late/stale callbacks are dropped by the determinism rule; forensics tolerate gaps; parent aggregation walks the RunScope tree, not the event log (2026-05-22, fan-out-safety-scope-first, artifact).
- `rimsky watch <id>` drains /events as a single timestamp-ordered chronological feed with kind/time filtering preserving order (2026-06-06, comprehensive-gap-closure, artifact; the CLI passes the server's opaque next_cursor token back and commits its dedup watermark only after the backlog drains — 2026-06-02, rimsky-core-remediation, artifact).
- compose run leaves a durable post-exit audit artifact including the event log, queryable with stock sqlite3 (2026-06-13/14, 65667e33 / f0176bde, transcripts).
- Reaping honors the shared trace-retention policy: trailing time window OR most-recent-frames count cap, whichever retains less; event logs age out by time only; count-cap applies to structural rows (2026-06-03, artifact).

## Intentional absences

- **The named-event ledger (`rimsky_node_events`), NamedEvent wire message, event/<name> signal arm, and `{{nodes.X.event.NAME}}` substitution.** Retired outright in the tags-on-terminals reversal — replaced by terminal/* signals with CEL tag filters and per-emission data riding attributes (2026-06-16 user decision, 055468fc; executed 2026-06-17, b31002b8, transcript, migration 013). This voids the May promises around declared_events registration gates, named-event blob spill, most-recent-emission substitution semantics, and the claude-agent `emit_named_event` MCP tool (2026-05-08 / 2026-05-11 / 2026-05-20 / 2026-05-28, artifacts).
- **SSE events streaming and breakpoint.hit emission onto the event log — for v1.** Poll-based /events and the pollable breakpoint-hits route are the accepted fallback for both the operator console and the debugger; live-tail is a UX improvement, not a capability gap (2026-06-18, 9fb55f08, transcript). This narrows the earlier artifact promises of breakpoint.hit rows on the unified feed (2026-06-06/08).
- **A persistence CHECK constraint or kind-registry table for event kinds.** Explicitly rejected alongside leaving kinds free-form; the app-boundary enum is the gate (2026-06-08, corpus-bootstrap, artifact).
- **A `reason` field on audit rows.** Declined; the action-to-audit cross-link is the console's job (2026-05-29, artifact).
- **Live event-log subscription as a debugger feedback surface** (2026-05-24, instance-debugger, artifact).
- **`ErrorPayload.action_index`** (events.proto field 4). Removed as unused cruft (2026-06-22, 10cf843b, transcript, user).
- **Async best-effort audit dispatcher.** The bounded worker pool that silently dropped rows under load was deleted; durability over latency (2026-05-29, artifact).
- **General "no built-in retention" invariant.** Explicitly replaced by shared trace-retention (2026-06-03, artifact reversal).

## Corrections and restorations (drift-fight record)

- The per-request audit row was async and silently droppable under load, contradicting the log's forensic-record framing; made synchronous and durable inline (2026-05-29, console-upstream-auth-audit-and-fixes, artifact).
- `work_completed` was declared in the catalog but never emitted — "a declared-but-never-emitted kind is a catalog lie" — fixed at terminal application (2026-06-11, last-mile-stability, artifact).
- The acquire/unavailable path wrote a legacy kind="error" hand-built row instead of a canonical signal; fixed so it emits canonical terminal/error/acquire/unavailable (or transient/retry/<n>/...) (2026-05-24, signal-taxonomy divergences, artifact).
- Audit-emission canonicalization finished beyond plan scope: fixed-string kinds pure_cascade_commit, named_event_emitted, park_requested, two policy-path error writes, and heartbeat_lost all retired for canonical signal emissions (2026-05-23, divergences, artifact).
- `rimsky watch` fabricated a numeric cursor (500 on first advance) and later double-printed breakpoint hits from two sources; fixed to pass the opaque token and drain /events alone (2026-06-02 + 2026-06-06, artifacts).
- Park-vs-work_completed: the concept catalog and code agreed; only story wording was stale — adjudicated fix-doc, finding 2348 (2026-07-13, 3f71f90a, transcript). Precedent: check whether code and concept already agree before ruling drift.

## Superseded / historical

- Two-table event-log split with opacity-bound named-event ledger (2026-05-11, log-convergence) → single rimsky_events after named-event retirement (2026-06-17).
- Free-form kind column with the events-kind-no-enum tension left open (2026-05-23) → typed OperationalKind enum at the app boundary (2026-06-08).
- TransitionReason as the audit vocabulary for all transitions (2026-05-11) → signal type-paths for signal-bearing transitions (2026-05-23).
- events.proto `store_name` audit fields → `producer_name` sweep, field numbers preserved (2026-05-13, nomenclature-resolution).
- `run_attempt` on AttributesSubstitutedPayload → removed pre-v1 with proto reservations (2026-05-20, attribute-pull-resolution).
- Source-grouped watch feed with free-text kind labels (2026-05-28, quality-of-life divergence) → single timestamp-ordered feed (2026-06-06, comprehensive-gap-closure).
- Breakpoint hits kept strictly out of rimsky_events (2026-05-24) → breakpoint.hit rows co-transactional on /events (2026-06-06, artifact) → emission onto the event log descoped for v1, pollable hits route authoritative (2026-06-18, transcript — transcript wins).
- No-retention invariant → shared trailing trace-retention window (2026-06-03).

## Conflicts needing human ruling

- Breakpoint-hit surfacing: the 2026-06-06/06-08 artifacts describe breakpoint.hit-on-/events as built and co-transactional, while the 2026-06-18 transcript declares that emission out of v1 scope with polling as the fallback. Precedence says the transcript wins for what v1 must have, but the record does not say whether already-landed emission was to be ripped out or merely not relied upon — an adjudicator facing a finding about breakpoint.hit rows on /events should get a human ruling on which state is canonical.
