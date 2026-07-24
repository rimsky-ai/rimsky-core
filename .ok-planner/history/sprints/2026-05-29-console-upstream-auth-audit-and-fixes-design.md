# Console-upstream: auth model, audit durability, and action fixes

**Date:** 2026-05-29
**Status:** Spec (approved-pending-review)
**Source sketches:** `sketch:2026-05-28-operator-console-rimsky-core-feature-requests`, `sketch:2026-05-28-event-trigger-payload-binding` (both fully dispositioned by this spec; archived on approval)
**Related:** `sketch:2026-05-29-reactive-nomenclature-rework` (separate future brainstorm)

## Overview

A batch of rimsky-core upstream changes, most of them needed by the operator-console
consumer, plus two correctness fixes and one folded-in dispatch fix. The unifying
theme is **rigor**: dry-run and permission are simplified into orthogonal primitives;
the audit log is made durable instead of best-effort-droppable; two silent-degradation
bugs are closed; and a half-built substitution feature is completed. Several changes
are equally doc-corrections to the design catalog, captured under `## Design changes`.

This spec covers **what to build**, not how to ship it. It executes end-to-end in one
plan; the streaming work (events SSE, breakpoint-hit emission) and the nomenclature
rework are explicitly out of scope (below).

## Scope

**In scope**
1. Permission becomes binary (drop the `mode` modifier).
2. Dry-run becomes a per-request flag (`?dry_run=true`), with uniform write coverage.
3. Event log durability (synchronous audit write; no silent drops).
4. Audit read surface (`GET /audit` + `audit:read`).
5. `lineage:prune` dry-run returns a real count.
6. `backfill:create` rejects a non-fan-out target.
7. Fix backfill partition-request overrides (silently broken today): fan-out acquisition substitutes its `partition_request` from the triggering message; coalesce delivery becomes conflict-aware; `FrameDeliveryMode` default flips to `serial_queue`.

**Out of scope (deferred to other work)**
- Events SSE + breakpoint-hit event emission → captured as a new sketch on approval; brainstormed with `sketch:2026-05-27-readonly-runscope-spine`.
- Action→audit cross-link and the terminate/reap teardown gesture → console-side; the only rimsky dependency (the `/audit` filter surface) is delivered by item 4.
- The reactive nomenclature rework (event→response, subscribe→watch, payload→body) → `sketch:2026-05-29-reactive-nomenclature-rework`, its own brainstorm.
- Per-emission event-payload binding (the dropped half of the event-trigger sketch) — see item 7.

**Pre-v1 posture:** per project rules, take the clean path. Existing grants carrying
`mode` are dropped (no compat shim); schema changes drop/recreate rather than thread
migrations where cleaner.

---

## 1. Permission becomes binary

**Today.** A grant entry is `{action, mode?}`; `mode` (`execute` | `dry_run`) is a
per-entry modifier (`concept:permission`, `concept:dry-run`). The evaluator is
first-match-wins specifically so the matching entry's `mode` can be resolved. The auth
middleware sets the resolved mode into the request context
(`code:lib/control/controlapi/auth_middleware.go`, `ctxKeyMode`), read back by
`code:lib/control/controlapi/auth.go::ModeFromContext`.

**Change.** A grant entry is just an **action string** (the existing wildcard grammar —
`*`, `<noun>:*`, `*:<verb>` — unchanged). Drop `mode` entirely. Permission evaluation
becomes **set membership**: a request is allowed iff any grant entry matches its action;
otherwise denied. First-match-wins is no longer meaningful for authorization (any match
allows) and is removed as a concept.

**Consequences.**
- The grant parser drops the `mode` field (`GrantEntry.Mode`). The forward-compatible
  "preserve unknown fields" behavior stays. Dropping the field is compile-affecting across
  its uses (next bullets).
- The CLI grant-patch operator changes: today `code:cmd/rimsky/cli/auth_create.go`'s
  `--dry-run=<action>` flag and `code:cmd/rimsky/cli/auth_common.go::applyGrantPatches`
  append `{action, mode: dry_run}` / `{action, mode: execute}` entries. **Remove the
  `--dry-run` grant flag entirely** (per-grant dry-run no longer exists) and strip the
  `Mode:` literal from the `--add` path; update `code:cmd/rimsky/cli/auth_common_test.go`
  (which currently asserts `got[1].Mode == auth.ModeDryRun`).
- Bundled role JSONs under `cmd/rimsky/cli/roles/` are **already** mode-free (entries are
  `{action}` only), so they need no entry changes — but see the `role-template` Design
  change for the now-false `--dry-run` / ignore-mode invariant text to correct.
- Existing persisted grants with `mode` are dropped (pre-v1; no shim).
- Mode no longer originates from the grant — it originates from the request (item 2).

---

## 2. Dry-run becomes a per-request flag

**Today.** Mode is fixed per grant (item 1). A key is permanently preview-only or
execute-only for an action. Handlers gate the side-effectful path on
`ModeFromContext`, emitting a synthetic `{dry_run:true, would_have_*}` envelope via
`code:lib/control/controlapi/dryrun.go::WriteDryRunResponse`. Coverage is partial:
`instance:pause`/`instance:resume`, all breakpoint writes, and the auth mutations have
no dry-run branch; `auth:create`/`revoke`/`rotate` ignore mode by deliberate design
(`code:lib/control/controlapi/auth_handlers.go::handleCreateKey`).

**Change — the flag.** Add a request-level `?dry_run=true` query parameter, honored by
the auth middleware: it sets the request mode to `dry_run` regardless of the (now
mode-less) grant. There is no preview-only key anymore, so there is nothing to escalate;
the flag is the *only* source of dry-run. Default (absent flag) is `execute`.

**Read actions — no-op preview.** A read has no mutation to skip. `?dry_run=true` on a
`*:read` action is honored as a no-op: the read runs and returns normally. This lets a
mixed read/write script set the flag uniformly without special-casing reads. The audit
row records `mode:dry_run` with `executed:true` (the read genuinely ran).

**Write actions — uniform coverage, no carve-outs.** **Every** write action must have a
dry-run branch, including `auth:create`/`revoke`/`rotate`. Add the missing branches:
- `instance:pause` / `instance:resume` (`code:lib/control/controlapi/instances.go`) — `would_have_paused` / `would_have_resumed` with the instance id.
- `breakpoint:create` / `breakpoint:resume` / `breakpoint:delete` (`code:lib/control/controlapi/breakpoints.go`) — `would_have_*` with the breakpoint/hit target.
- `auth:create` / `auth:revoke` / `auth:rotate` — remove the "ignore mode" carve-out. `auth:create` dry-run returns a placeholder id (mirroring `instance:create`'s `"dry-run-not-persisted"`) and mints **no** plaintext credential. In `concept:anonymous-mode` (zero active keys), the `auth:create` dry-run response notes that committing the first key exits anonymous mode and requires auth on all future requests.

**`executed` semantics.** The audit payload's `executed` becomes: for a read,
`status < 400` (it ran); for a write, `mode == execute && status < 400`. The registry's
`IsWrite` flag discriminates — which means the gate must pass it into the audit emit:
today `code:lib/control/controlapi/auth_middleware.go` passes only the `action` string to
the emit helper, so it gains `IsWrite` via `Registry.Entry(action).IsWrite`.

**Structural guarantee (the safety invariant).** A request resolved to `dry_run` must
never produce a live mutation. With no carve-outs, this holds by construction. Enforce
it with a **coverage conformance test** (testing strategy below) that enumerates every
write action in `code:lib/control/controlapi/actions.go::BuildV1Registry` and asserts
each, invoked with `?dry_run=true`, performs no mutation and returns a `would_have_*`
envelope. No runtime registry flag or gate is needed — the test fails CI if a future
write handler forgets its branch.

**Narrative removal.** The "supervise an agent on a preview-only key, then promote it"
rationale is removed from `concept:dry-run` (see Design changes). Dry-run's purpose
becomes human-in-the-loop preview-before-commit and validate-without-commit.

---

## 3. Event log durability

**Today.** The per-request audit row (`auth.access_attempted`) is written
**asynchronously and best-effort** through a bounded worker pool that **drops** the row
under load (`code:lib/control/controlapi/audit.go::auditDispatcher`, `submit` drops on a
full queue). This was an unsurfaced implementation default; it contradicts
`concept:event-log`'s framing of the log as the canonical, forensic record (and
dry-run's "evidence of intent").

**Change.** Make the audit write **durable and synchronous**. Delete the
`auditDispatcher` and its bounded queue, including the `auditDisp` field and the
`EnsureAuditDispatcher` / `StopAuditDispatcher` / `dispatcher()` methods on
`code:lib/control/controlapi/auth_middleware.go::AuthState`, and their call sites in
`code:lib/control/config/controlapi.go` (the startup `EnsureAuditDispatcher` and the
shutdown `StopAuditDispatcher`). The middleware writes the `auth.access_attempted` row
inline in the request goroutine — *after* the handler returns (so `response_status` and
`duration_ms` are known) and before the gate returns. The marshal/transaction-bridge
logic in `insertEvent` is retained; only the async/droppable layer is removed. (The
`auth.access_denied` rows are already written synchronously, before their 401/403
response, on the pre-handler denial paths; they keep that shape.)

**Tradeoff (recorded deliberately):** each authenticated control-api request now waits
on one small `INSERT`. At control-plane traffic volume (operator/agent, not a high-QPS
data plane) this is negligible against the handler work already done. We are choosing a
hair of per-request latency over the standing complexity of a durable-outbox, and over
the silent-drop hole. Operational event writes (node transitions, lock/work events) are
already written synchronously inside the supervisor's transactions and are unaffected.

---

## 4. Audit read surface

**Today.** Audit rows are `auth.*`-kind rows in `table:rimsky_events`. The only reader
is `route:GET /events` (`code:lib/control/controlapi/events.go`), which filters by
`kind`/`instance`/`node`/`time` + cursor — the top-level columns. The actor, action,
result, and mode live inside the JSONB `payload` and are not filterable.

**Change.**
- New action **`audit:read`** in `code:lib/control/controlapi/actions.go::BuildV1Registry`, distinct from `event:read` (audit data — actor identity, IP, user-agent, actions — is sensitive enough to grant separately). Added to the read-only / operator / admin role-templates.
- New read route **`route:GET /audit`**, following the `/events` pattern (bare unversioned path; short fresh transaction per `concept:cascade-graph` discipline), gated by `audit:read`. Queries `rimsky_events WHERE kind LIKE 'auth.%'`. Filters: actor (`key_id` / `key_name`), action (exact or wildcard), target path, time range, result (`response_status`), `mode`; cursor pagination consistent with `/events`.
- **Query shape:** extend `code:lib/control/controlapi/events.go`'s `EventListFilter` (today only `Kind` / `KindIn` / `InstanceID` / `NodeID` / `Since` / `Until`) with the auth-payload filter fields (`key_id`, `key_name`, action exact/prefix, `response_status`, `mode`), and implement the new filtering in **both** the postgres and sqlite `EventTable.List` drivers plus the persistence conformance suite. No dedicated audit table — `auth.*` stays in `rimsky_events`.
- **Expression indexes** on the `auth.*` payload fields used for filtering, added in both `lib/foundation/persistence/postgres/migrations/` and `.../sqlite/migrations/` — partial indexes scoped to `kind LIKE 'auth.%'` over `payload->>'key_id'`, `payload->>'action'`, `payload->>'response_status'`, `payload->>'mode'` (Postgres), and the `json_extract(payload, '$.…')` equivalents (SQLite, modernc). `occurred_at` is already indexed.

No `reason` field (declined). The action→audit cross-link is console-side: the dashboard
queries `/audit` filtered by (actor, action, target, since=request-time).

---

## 5. `lineage:prune` dry-run count (F6)

**Today.** `code:lib/control/controlapi/lineage.go::handleLineagePrune` dry-run returns
`would_have_pruned: {before: <timestamp>}` — just an echo. The live path calls
`code:lib/foundation/persistence/lineage.go::LineageTable.DeleteOlderThan` and reports
the deleted count.

**Change.** Add `CountOlderThan(ctx, cutoff) (int, error)` to `LineageTable` (Postgres +
SQLite + conformance). The prune dry-run runs it and returns
`would_have_pruned: {before, count: N}`. Exact count (a single indexed count over
`observed_at`); not an estimate. Prune remains synchronous (no cancel — confirmed not
long-running).

---

## 6. `backfill:create` target validation (F7)

**Today.** `code:lib/control/controlapi/backfills.go` validates only that `target_node`
is non-empty. `concept:backfill`'s invariant says it should *warn* when the target's
`fan_out.partition_request` isn't wired for the override; `code:lib/runtime/backfill.go`
defers target validation to "the control-api layer's concern," which never does it. A
backfill against a non-fan-out target silently degrades to a plain invalidate — the
operator's `partition_request_override` is ignored while the call returns `201`.

**Change.** `backfill:create` **rejects with `400`** a `target_node` that is not a
fan-out node wired to accept the partition override, with a clear message naming the
requirement. A backfill is meaningless without a partition (and thus a fan-out), so it is
refused at submit rather than silently no-op'd. This tightens `concept:backfill`'s
invariant from "warn" to "reject" (Design changes). The dry-run branch projects the same
validation (a bad target fails the same way in preview).

---

## 7. Fix backfill partition-request overrides (the silent bug)

**The bug.** A backfill is a regular `invalidate` message carrying
`{partition_request_override, backfill_operation_id, reason}`, targeting a fan-out node
(`concept:backfill`). The fan-out node's `partition_request` is authored to pull the
override from the triggering message (`concept:fan-out`, canonical form
`{{trigger.message.payload.partition_request_override | <template-default>}}` — note the
`|`-fallback grammar is `<directive> | <literal>`; there is no `default:` keyword).
But the override **never reaches the node in production**: fan-out acquisition
(`code:lib/runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared`) passes
`nodeDef.FanOut.PartitionRequest` to `AcquireSubClaims` (which hands it to the producer's
`SplitScope`) **verbatim, un-substituted** — the in-code comment admits "until the
substitution-aware caller lands, the literal bytes … flow through verbatim." So
`{{trigger.message.payload…}}` is never resolved, the `|`-fallback to the template default
always fires, and **every backfill silently processes the template default and ignores the
operator's override.** Same silent-degradation class as the audit drop, masked by the
`|`-fallback so it never even errors.

**Fix — three parts.**

**(a) Make fan-out acquisition substitution-aware.** In `acquireFanOutIfDeclared`, before
calling `AcquireSubClaims`, run `nodeDef.FanOut.PartitionRequest` through the substitution
engine (`code:lib/graph/attribute/substitution.go::SubstituteValue`) with a `ResolveContext`
that includes the frame's delivered-message payload, and pass the **substituted** bytes to
`AcquireSubClaims`. This is the "substitution-aware caller" the comment anticipated. The
`frameID` is in scope here; the message is reachable by `frame_id` because delivery marks
`frame_id` and invalidates the node *before* the node-run is acquired, so the row exists at
acquisition (the implementer verifies this ordering). The params/claims already reachable
at acquisition go into the context too.

**(b) By-frame message lookup.** Extend `code:lib/foundation/persistence/messages.go`'s
`MessageListFilter` with a `FrameID *shared.UUID` field; implement the `frame_id = ?`
predicate in both the postgres and sqlite `List` + the persistence conformance suite.
`acquireFanOutIfDeclared` uses it to fetch the frame's delivered message(s).

The substitution directive keeps its current spelling (`{{trigger.message.payload…}}`) for
now — "trigger" is a redundant single-member namespace and is renamed via
`sketch:2026-05-29-reactive-nomenclature-rework`, not here.

**(c) Coalesce delivery becomes conflict-aware; serial_queue becomes the default.** With
(a)+(b) the override reaches the node — but only unambiguously if the frame carries one
override. Both decisions are on **`FrameDeliveryMode`** (per-instance message delivery,
`col:rimsky_instances.frame_delivery_mode`) — explicitly **not** `FrameResolutionMode` (a
separate, template-driven frame-aggregation knob on `rimsky_frames`, untouched here):
- **Default → `serial_queue`.** Flip the column default in both migrations and the code
  fallback in `code:lib/runtime/message_delivery.go::deliverForRunningFrame` from
  `coalesce` to `serial_queue`. serial_queue delivers one message per frame
  (`DeliverPendingMessages` already does `deliverSet = live[:1]`), so each backfill is its
  own frame/rerun/override — unambiguous, and the intuitive default. Coalesce becomes the
  opt-in "fancy" mode.
- **Coalesce becomes conflict-aware.** Today coalesce delivers *all* pending messages into
  one frame (`deliverSet = live`), which collides distinct overrides. Change it to:
  deliver pending messages **in strict received-order, coalescing until a message would
  resolve a node's substitution to a *conflicting* (different) value, then stop** — the
  rest stay pending for the next frame. Same-value bindings are idempotent and coalesce
  freely; only a genuine value-disagreement breaks the frame. Conflict detection reuses the
  subscription/substitution-ref machinery the delivery cascade already loads
  (`cascadeMessageSubscribersInTx` loads the template and calls
  `ExtractSubstitutionRefsFromTemplate` / `BuildSubscriptionEdges`): a message conflicts iff
  it would give a payload-reading node a different trigger-message body than one already
  accumulated in this frame. This requires computing per-message subscriber/substitution
  matches *as part of* the deliver-set decision (today they run after delivery) — a bounded
  restructure of `DeliverPendingMessages` / `deliverForRunningFrame`.

**Result.** Backfill overrides work. Under the default (serial_queue) each backfill is its
own frame, processed in order; under coalesce, distinct overrides break into consecutive
frames rather than colliding, while idempotent/duplicate messages still coalesce. F7
(item 6) validates that the target's `partition_request` actually reads the override.

**Dropped (unchanged).** The event-trigger sketch's per-emission `{{trigger.event.payload}}`
is not built — named events don't fan out per-emission (the wait-set `ON CONFLICT` +
shared cascade `visited` set collapse N emissions to one dispatch), so there is no
per-dispatch triggering emission to bind. True per-item fan-out is `concept:fan-out`
(claim-producer split-scope); sequential per-message processing is serial_queue delivery.

---

## Error handling

| Situation | Behavior |
|---|---|
| `?dry_run=true` on a read | `200`, read runs (no-op preview); audit `mode:dry_run, executed:true` |
| `?dry_run=true` on a write | `200 {dry_run:true, would_have_*}`; no mutation; audit `executed:false` |
| Write action with no dry-run branch (should be impossible) | Prevented structurally by the coverage conformance test, not at runtime |
| `audit:read` route without grant | `403` (standard auth) |
| `backfill:create` non-fan-out / unwired target | `400` naming the requirement (must be a fan-out node wired for the override) |
| `lineage:prune` dry-run | `200 {dry_run:true, would_have_pruned:{before, count:N}}` |
| `{{trigger.message.payload.X}}` under coalesce-with-multiple, or no trigger message | `ErrMissingSource` (refuse, don't guess) |

---

## Testing strategy

The load-bearing properties get explicit, exercising tests (not happy-path checks):

- **Dry-run coverage conformance test** — enumerate every write action in
  `BuildV1Registry`; for each, invoke with `?dry_run=true` and assert (a) no mutation
  occurred and (b) a `would_have_*` envelope returned. This *is* the "forced dry-run
  never mutates" guarantee and fails CI if a future write handler omits its branch.
- **Audit durability test** — after an authenticated request returns, its
  `auth.access_attempted` row is present (synchronous; there is no drop path to test
  around). Include a concurrent-load variant to confirm no drops.
- **Permission evaluator test** — binary allow/deny over wildcard grants; no `mode`.
- **Dry-run read no-op test** — `?dry_run=true` on a `*:read` returns the normal read; audit row has `mode:dry_run, executed:true`.
- **Auth-mutation dry-run tests** — `auth:create`/`revoke`/`rotate` under `?dry_run=true` mutate nothing; `auth:create` returns a placeholder and no plaintext; anonymous-mode `auth:create` dry-run carries the lockdown note.
- **Prune count test** — dry-run `count` equals the live `deleted` count for the same cutoff.
- **Backfill reject test** — non-fan-out / unwired target → `400`; valid wired fan-out target → accepted.
- **Backfill override test** — a backfill's `partition_request_override` actually reaches the fan-out node: the partitions processed reflect the override, not the template default (the regression test for the silent bug).
- **Coalesce conflict test** — under `coalesce`, two backfills with *different* overrides break into consecutive frames (both processed in order, neither lost); two messages with the *same* override coalesce into one frame.
- **Serial_queue default test** — a freshly-created instance (no explicit mode) defaults to `serial_queue` and delivers one message per frame.
- **Audit query test** — `/audit` filters by actor/action/result/mode/time return the expected rows; `audit:read` gating enforced.

Standard gates apply per project rules (`go build ./... && go test ./... && make lint`;
scenario/persistence tests under testcontainers; `-race` on runtime/queue paths;
`make proto-gen` if any proto changes — none expected here).

---

## Design changes

- **Concept: `concepts/dry-run.md` — rewrite.** Replace "What it is" so dry-run is a
  per-request flag (`?dry_run=true`), not a per-grant-entry `mode` modifier. Rewrite
  "Purpose" to human-in-the-loop preview-before-commit + validate-without-commit;
  **remove the graduated-trust / agent-promotion narrative** entirely. Rewrite
  "Boundaries" so dry-run covers **all** write actions (no auth carve-out) and no longer
  owns a per-grant mode vocabulary. Rewrite "Invariants": reads honor the flag as a
  no-op with `executed:true`; every write is previewable, guaranteed structurally by a
  coverage conformance test; a request in `dry_run` never mutates; **remove** "Auth
  mutations are NOT dry-runnable." Append a Notes entry dated 2026-05-29 citing
  `spec:2026-05-29-console-upstream-auth-audit-and-fixes`.
- **Concept: `concepts/permission.md` — reshape.** "What it is": a grant entry is an
  action string (wildcards unchanged); no `mode` modifier. "Invariants": replace
  first-match-wins-for-mode with set-membership evaluation (any matching entry allows);
  **remove** "Read actions ignore mode" and "Auth mutations are NOT dry-runnable"; add
  `audit:read` to the canonical action list. Drop the `concept:dry-run` adjacency-as-mode
  framing. Notes entry dated 2026-05-29.
- **Concept: `concepts/event-log.md` — durability + reader.** Add an invariant: audit/event
  writes are **durable — never silently dropped**; the per-request auth-audit write is
  synchronous in the request path. Add `audit:read` as a reader of the `auth.*` rows.
  Notes entry dated 2026-05-29.
- **Concept: `concepts/backfill.md` — tighten invariant + the override now functions.**
  Change the target-validation invariant from "warning if not [a fan-out node wired for the
  override]" to "`backfill:create` **rejects (400)** a target that is not a fan-out node
  wired to accept the partition override." Also correct the override description: the
  `partition_request_override` now actually reaches the fan-out node — fan-out acquisition
  substitutes the node's `partition_request` from the triggering message at acquisition time
  (previously it passed the template's `partition_request` verbatim, so the override was
  silently ignored and the `|`-fallback always fired). Notes entry dated 2026-05-29.
- **Concept: `concepts/fan-out.md` — partition_request is substituted at acquisition +
  fix the directive grammar.** State that a fan-out node's `partition_request` is resolved
  through substitution at acquisition (`{{trigger.message.payload.partition_request_override
  | <template-default>}}` binds the triggering message's override), not passed verbatim.
  **Also fix the pre-existing doc bug**: the current text uses `| default: <x>`, but the
  engine's fallback grammar is `<directive> | <literal>` (literal = `null`/`true`/`false`/
  number/quoted-string) — there is no `default:` keyword; drop it from the illustrative
  directive. Notes entry dated 2026-05-29.
- **Concept: `concepts/message.md` — delivery default + coalesce semantics.** `message.md`
  is the owner of `frame_delivery_mode` (its Definition, Boundaries "Owns: … the delivery
  semantics," and Invariants all assert coalesce-default). Two changes, both on
  **`FrameDeliveryMode`** (per-instance message delivery), explicitly distinguished from the
  separate template-driven **`FrameResolutionMode`** (frame aggregation, unchanged): (1) the
  default flips from `coalesce` to **`serial_queue`** (one message per frame; the intuitive
  default; coalesce is opt-in); (2) `coalesce` delivers pending messages **in received-order,
  coalescing until a message would resolve a node's substitution to a conflicting (different)
  value, then breaks into the next frame** — same-value bindings are idempotent and coalesce
  freely. Update **all three** spots that currently say/imply coalesce-default and
  coalesce-delivers-all (Definition, Boundaries, Invariants), not just one. Notes entry dated
  2026-05-29.
- **Concept: `concepts/frame.md` — cross-reference correction.** `frame.md` only mentions
  `frame_delivery_mode` as a cross-reference to `concept:message`; correct any parenthetical
  that names `coalesce` as the default to `serial_queue`. (`frame.md`'s own owned knob is
  `FrameResolutionMode`, unchanged.) Notes entry dated 2026-05-29 only if edited.
- **Concept: `concepts/role-template.md` — correct the dry-run invariant.** The bundled
  role JSONs are already mode-free, so no entry text changes — but the concept currently
  describes a `--dry-run=<action>` grant-patch operator (Boundaries) and an invariant that
  it "rejects auth mutations because the handlers ignore dry-run mode anyway." Both are now
  false: the `--dry-run` CLI grant operator is removed (per-grant dry-run no longer exists),
  and auth mutations are dry-runnable via the request flag. Remove the `--dry-run`
  patch-operator description from Boundaries and delete the ignore-dry-run-mode invariant.
  Notes entry dated 2026-05-29.
- **Concept: `concepts/named-event.md` — accuracy fix.** State plainly that a named event
  is consumed **invalidate-then-pull**: subscribing fires the receiver **once per frame
  regardless of emission count**, the receiver pulls the **latest** emission via
  substitution, and named events **never create a frame** and do **not** fan out
  per-emission. Add that **named events are not a fan-out mechanism**: true per-item
  (parallel) fan-out is `concept:fan-out` (claim-producer split-scope); sequential
  per-message processing is `serial_queue` message delivery (see `concept:message`).
  Soften any "carries a payload" phrasing that implies delivery. Notes entry dated
  2026-05-29.
- **Concept: `concepts/node-subscription.md` — accuracy fix.** Correct the 2026-05-20
  "subscriptions remain push: an upstream transition causes the receiver to fire" line:
  the receiver is **invalidated and rescheduled, then pulls** the latest persisted values;
  nothing rides the cascade edge. State the event-subscription cardinality (one dispatch
  per frame, latest-only). Notes entry dated 2026-05-29.
- **Tension: create `tensions/event-vocabulary-implies-delivery.md`** (status: open;
  `affects: [named-event, node-subscription]`). What is muddy: the pub-sub vocabulary
  ("event", "subscribe", "subscriber", "push", "carries a payload") models a
  delivery system, but the engine is invalidate-then-pull / reactive-recompute; the
  mismatch has already misled an agent into a wrong design (the dropped per-emission
  event-payload feature). Resolution candidates (must be written **path-free** into the
  tension file — no sketch/spec/file references): rename the reactive vocabulary toward
  invalidation/reactive terms (event→response, subscribe→watch, payload→body), decided in
  a future `/refine-design`; this spec only corrects the existing docs, it does not rename.
  (The exploratory rework lives in the `2026-05-29-reactive-nomenclature-rework` sketch —
  noted here in the spec for the reader, **not** to be copied into the tension body.)

`execute-plan` adds/adjusts `@concept:` annotations at the reshaped sites (the dry-run
flag handling in the auth middleware, the set-membership permission evaluator, the
synchronous audit write, the `/audit` route, the backfill validation, the fan-out
acquisition `partition_request` substitution, and the conflict-aware coalesce delivery /
`serial_queue` default).
