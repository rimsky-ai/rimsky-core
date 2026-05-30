# Operator Console — rimsky-core feature requests

**Date:** 2026-05-28
**Status:** Feature list (upstream asks; not a spec, not a design)
**Supports:** `sketch:2026-05-27-operator-console` (and, where flagged,
`sketch:2026-05-27-auth-and-multi-cluster` and
`sketch:2026-05-27-readonly-runscope-spine`)

## Purpose

The operator-console sketch surfaces the dashboard's full write surface.
Most of what it needs already exists in rimsky-core's control-api. This
doc is the residue: the things rimsky-core must add or change before the
sketch can be implemented in full. We implement these in a separate
rimsky-core session, then return here to brainstorm the sketch against a
known-good upstream.

**Feature *design* is deliberately not fine-tuned here.** That happens in
the rimsky-core session. This doc fixes *what* is needed and *why*, pins
the *verified current state* of rimsky-core, and flags the open design
decisions for the rimsky-core session to resolve. Recommendations are
marked "lean:" and are non-binding.

## rimsky-core state reviewed

Reviewed at commit `3ebe87a` plus the staged-but-uncommitted
`spec:2026-05-28-quality-of-life-features` plan (5 features:
template-lint, instance-kill, breakpoint-hits REST route, instance
status/watch CLI, claude-agent named-event emission).

Ground truth from that review:

- **All 25 operator write-actions already have HTTP+JSON routes**, gated
  through the canonical action registry (`code:lib/control/controlapi/actions.go::BuildV1Registry`).
  The premise "rimsky-core already exposes every write" holds.
- **Already shipped by the QoL plan — do NOT rebuild:**
  - `template:validate` — `route:POST /templates/validate`, read-shaped
    (`IsWrite:false`), runs the full register validation pipeline (static
    attribute-schema check + the validation-protocol RPC fan-out) without
    persisting; returns `{ok, validation_errors, validation_warnings}` at
    **HTTP 200** even on findings. The register multi-step flow's
    validation step should use this, not register-dry-run (narrower
    permission, cleaner semantics).
  - `instance:kill` — `route:POST /instances/{idOrKey}/terminate`, the
    first production force-terminate path: marks the instance terminal,
    force-fails in-flight node-runs to `failed`
    (`transition-reason:instance_killed`), abandons in-flight claims.
    **Has a dry-run branch** (`would_have_terminated`, listing the
    node-runs that would be force-failed) and accepts an optional
    `{reason}` recorded on an `instance_terminated` event-log row.
  - `route:GET /instances/{idOrKey}/breakpoint-hits` — installed-hit
    ledger now readable over HTTP (`?since=<seq>&limit=<n>`, returns
    `{hits, next_since, truncated}`), under the existing `breakpoint:read`
    action. Earlier sketches feared this was MCP-only; it is not.
- **Already-present dry-run coverage** (16 actions): instance
  create/terminate(DELETE)/kill, template register/deploy/undeploy/deregister,
  tag create/set/delete, node invalidate/reset, message send, lineage
  prune, backfill create/cancel, asset materialize/delete.
- **Register-dry-run already returns per-validator-service findings**
  (`{ServiceName, Role, NodeAlias, Class, Message, Path}`), plus the
  `warnings_as_errors` query knob and the `unreachable_validator_policy`
  setting. The register flow's validation panel is fully backed.
- **Reason is already audited for tolerant-decode writes**: the auth
  middleware captures the request body verbatim into the audit row's
  `request_params` (`code:lib/control/controlapi/auth_middleware.go`), and
  every write handler except `template:register` decodes tolerantly (only
  `code:lib/control/controlapi/templates.go` uses `DisallowUnknownFields`).
  So a `reason` posted in the body already lands on the audit row today.
- `node:invalidate` and `instance:terminate`/`kill` accept and persist a
  `reason`; `node:reset`, `lineage:prune`, pause/resume, and most others
  do not (but see the request_params capture above).

## Feature asks

Grouped by area. Each: **Need**, **Why the console needs it**, **Verified
state**, **Open (defer to rimsky-core session)**, **Scope**.

### A — Dry-run

#### F1 — Request-level dry-run override (the keystone)

- **Need:** a request-level way to force `dry_run` for a single call —
  `lean: ?dry_run=true` query param (alt: `X-Dry-Run` header) — that the
  auth middleware honors by **downgrading** a resolved `execute` mode to
  `dry_run`, and **never** upgrading `dry_run`→`execute`.
- **Why:** the sketch's two-click Preview→Execute discipline requires one
  operator key to both preview (click 1) and execute (click 2). Today
  dry-run is resolved strictly from the matching grant-entry's `mode`
  (first-match-wins over the key's grant), so a key is *either*
  preview-only *or* execute-only. The downgrade-only rule preserves
  `concept:dry-run`'s preview-only-key invariant for agent supervision (a
  preview-only key can never escalate to a live write).
- **Verified state:** no override exists. `code:lib/control/controlapi/auth_middleware.go`
  sets `ctxKeyMode` only from `res.Mode`; `code:lib/control/controlapi/auth.go::ModeFromContext`
  reads only that. `dry-run.md` still defines mode as a per-grant-entry
  modifier. The only nearby query param is `warnings_as_errors` (a
  validation-strictness knob, unrelated).
- **Open:** query param vs header; exact downgrade semantics and how the
  audit row records an overridden dry-run; whether the override is
  rejected (not silently ignored) on read actions.
- **Scope:** console-keystone. Blast radius small — one middleware change;
  every existing dry-run handler already reads mode from context.
- **Concept touched:** `concept:dry-run` (boundary + a new
  downgrade-only invariant).

#### F2 — Dry-run branches for pause / resume / breakpoint create·resume·delete

- **Need:** add `WriteDryRunResponse` branches (with appropriate
  `would_have_*` envelopes) to the five writes that lack them.
- **Why:** uniform Preview discipline. Without them the "always preview"
  floor is ragged — some non-destructive buttons can preview, others
  can't. The high-frequency ones (`node:invalidate`/`reset`) are already
  covered; `instance:kill` is too.
- **Verified state:** `instances.go` has no dry-run branch for
  `instance:pause` / `instance:resume`; `breakpoints.go` has no dry-run
  handling at all. `dry-run.md`'s enumeration omits all five.
- **Open:** whether to cover all five or accept the ragged edge for the
  breakpoint-session writes (lower preview value than pause/resume).
- **Scope:** console. Additive per-handler work.
- **Concept touched:** `concept:dry-run` (enumeration extends).

### B — Audit

#### F3 — Audit-log read endpoint + `audit:read` action

- **Need:** `lean: route:GET /audit` gated by a **new `audit:read`
  action**, filterable by actor (key id/name), action (wildcard), target
  path, time range, success/mode, with cursor pagination. New action added
  to the registry + the read-only/operator/admin role-templates.
- **Why:** the audit-log viewer (auth sketch) and the console's
  post-action cross-link both need to read audit rows by the fields that
  matter. Today the rows land in `table:rimsky_events` with
  `kind = auth.access_attempted`, but the only reader
  (`route:GET /events?kind=auth.access_attempted`) filters by
  kind/instance/time — **not** by actor/action/result, which live inside
  the JSONB payload.
- **Verified state:** no `audit:read` action and no `/audit` route exist.
- **Open:** dedicated `/audit` vs extending `/events` with payload-field
  filters (lean: dedicated — keeps the auth-event taxonomy and
  payload-aware filters in one place; `/events` stays the operational
  event log).
- **Scope:** **shared** with the auth sketch (which owns the viewer). The
  console consumes it for the cross-link.
- **Concept touched:** `concept:permission` (new action),
  `concept:event-log`.

#### F4 — Cross-link from a write to its audit row

- **Need:** a way for the console to deep-link from a completed action to
  the audit row it produced.
- **Why:** the sketch's "response carries cross-link to audit row."
- **Verified state:** structurally hard, not the "small additive change"
  the sketch assumed. The audit row is written **after** the handler
  (`code:lib/control/controlapi/auth_middleware.go`), **asynchronously**
  through a bounded best-effort worker pool that can **drop** the row under
  load (`code:lib/control/controlapi/audit.go::auditDispatcher`), and
  `Append` discards the inserted id. Returning a by-id deep-link fights the
  deliberate `@concept:event-log` "audit never touches request latency"
  invariant.
- **Open / two paths:**
  - **(a) lean — filter-based, zero rimsky-core change.** After a write,
    the dashboard navigates to `/audit` filtered by (actor=me, action,
    target-path, since=request-time) and lands on the matching row. Robust
    to the async/droppable nature. Depends on F3.
  - **(b) by-id.** Make writes' audit rows synchronous and surface
    `X-Audit-Event-Id`; gives up the off-hot-path latency invariant for
    writes and removes their droppability.
- **Scope:** console. Lean (a) means **no** rimsky-core work here beyond
  F3.
- **Concept touched:** `concept:event-log` (only if (b)).

#### F5 — First-class reason attribution (polish, optional)

- **Need:** add a `Reason` field to the `auth.access_attempted` payload,
  captured by the middleware from an `X-Rimsky-Reason` header.
- **Why:** makes "why" a queryable first-class field instead of buried in
  `request_params`, and works for `template:register` (strict decode)
  where an in-body reason can't ride along.
- **Verified state:** reason is already auditable via `request_params` for
  every write except register; no first-class reason field on the audit
  payload.
- **Open:** do this (small middleware addition) vs ship v1 on the
  `request_params` capture (lean: do it — small, and the audit viewer's
  "why" column becomes first-class and filterable).
- **Scope:** console (polish). Non-blocking.
- **Concept touched:** `concept:event-log`.

### C — Action-specific

#### F6 — `lineage:prune` dry-run row count

- **Need:** the prune dry-run branch returns "would prune N rows" (a
  COUNT), not just an echo of `before`.
- **Why:** the sketch's prune preview wants the count.
- **Verified state:** dry-run returns only `would_have_pruned: {before}`
  (`code:lib/control/controlapi/lineage.go`); the live path computes the
  count via `DeleteOlderThan`. Also confirmed: prune is **synchronous** —
  not long-running, no cancel — which closes the sketch's "cancel a
  long-running prune" open question (answer: not needed).
- **Open:** exact count vs cheap estimate.
- **Scope:** console (preview quality). One handler branch.
- **Concept touched:** `concept:lineage`, `concept:dry-run`.

#### F7 — `backfill:create` rejects non-fan-out target (bug fix)

- **Need:** the create handler validates that `target_node` is a fan-out
  node, rejecting non-fan-out targets at submit.
- **Why:** the sketch's backfill dialog states "non-fan-out targets
  rejected by control-api at submit per `concept:backfill`," and the
  concept doc agrees — but the code does not validate it.
- **Verified state:** `code:lib/control/controlapi/backfills.go` checks
  only that `target_node` is non-empty; `code:lib/runtime/backfill.go`
  says target validation "is the control-api layer's concern," which the
  control-api layer does not do. This is a bug per rimsky-core's own "fix
  every bug you find" rule.
- **Open:** error shape (400 with which message).
- **Scope:** console relies on it. One handler validation.
- **Concept touched:** `concept:backfill`.

#### F10 — Model instance teardown as two actions (and decide on kill+purge)

- **Need:** the console must treat force-terminate and reap as distinct,
  and decide whether to offer a combined gesture.
- **Why:** the QoL plan split teardown. `instance:kill`
  (`route:POST /instances/{idOrKey}/terminate`) force-terminates a running
  instance; `instance:terminate` (`route:DELETE /instances/{idOrKey}`) is
  now the **reaper** that removes the row and frees `instance_key`, and
  only succeeds once the instance is already terminal. The sketch's single
  "terminate" row mapping to `DELETE` is wrong.
- **Verified state:** both actions exist; `instance.md` documents the
  terminal-vs-removal split. `instance:kill` has dry-run + reason. The QoL
  spec notes "a future `kill --purge` that chains the delete is an easy
  addition but is out of scope."
- **Open / two paths:**
  - **(a) lean — client-side, no rimsky-core change.** The dashboard
    surfaces "Terminate" → `instance:kill`, and a separate "Remove / free
    key" → `DELETE`; a combined "Terminate and remove" runs kill-then-delete
    client-side (consistent with the sketch's client-side bulk loops).
  - **(b)** add a server-side `?purge=true` on the kill route that chains
    the delete.
- **Scope:** console. Lean (a) means no rimsky-core work; (b) is a small
  additive ask.
- **Concept touched:** `concept:instance` (already updated for the split).

### D — Live observation (shared, mostly owned by other sketches)

#### F8 — Events SSE

- **Need:** an SSE variant of the events endpoint (`text/event-stream`,
  `lean: ?follow=true` or `/events/stream`) with last-event-id gap-fill on
  reconnect.
- **Why:** the console's "action → target → live observation" wants live
  transitions without polling; the spine sketch's live-tail is built on it.
- **Verified state:** `route:GET /events` is poll-only (`since`/`until`/
  `cursor`); no streaming. The QoL `rimsky watch` is a client-side poll
  loop — confirming no SSE was added.
- **Open:** route shape; reconnect/gap-fill protocol; cardinality concerns
  on a cluster-wide tail.
- **Scope:** **shared** — load-bearing for the spine sketch; for the
  console it is a degradation (poll/refresh) if absent.
- **Concept touched:** `concept:observability`.

#### F9 — Breakpoint-hit push (optional)

- **Need:** real-time delivery of breakpoint hits (vs polling).
- **Why:** the debugger UX wants an immediate "runner is PAUSED" pop.
- **Verified state:** hits are HTTP-pollable today (`route:GET /instances/{idOrKey}/breakpoint-hits`);
  there is no push and no `breakpoint.hit` event on the events stream.
- **Open / lean:** emit hits as `table:rimsky_events` rows
  (kind `breakpoint.hit`) so F8's SSE delivers them uniformly; until then,
  poll. Lower priority — the sketch already accepts polling as the
  fallback.
- **Scope:** console (debugger). Optional.
- **Concept touched:** `concept:breakpoint`, `concept:event-log`.

## Non-asks (open questions closed by the review)

- **"Skip preview" preference** — no server-side user-preferences store
  exists, and shouldn't. The dashboard owns this preference (its own
  session cookie / localStorage). No rimsky-core work.
- **Lineage-prune cancel** — prune is synchronous; no mid-operation cancel
  is needed (folded into F6's verified state).
- **Instance-create dynamic form** — dashboard-side. The only rimsky
  dependency is that `template:read` exposes the template's `params` JSON
  Schema; confirm that exposure in the rimsky-core session (low risk),
  then the form generator is purely dashboard work.
- **Template-register validation transport** — already solved by the
  shipped `template:validate` read endpoint; the register flow's
  validation step uses it. Not an ask.

## Scope decision for the rimsky-core session

The asks split into **console-only** (F1, F2, F4, F5, F6, F7, F10) and
**shared with the other two sketches** (F3 audit-read — auth sketch; F8
events-SSE — spine sketch). Decision to confirm at the start of the
rimsky-core session:

- **Lean: do the console-only set plus F3 and F8 in one pass.** F3 and F8
  are blocking dependencies the console consumes, and they are cheaper to
  land alongside the rest than to revisit. F9 is optional and can ride F8
  or defer.

## Relationship to the sketch

`sketch:2026-05-27-operator-console` was updated alongside this doc to
reflect the verified upstream reality (the terminate→kill split, the
`template:validate` endpoint, the resolved open questions, and pointers
into this feature list). The sketch remains pre-spec; this doc is the
upstream-work prerequisite for it.
