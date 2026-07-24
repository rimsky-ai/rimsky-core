# Design log convergence

**Spec slug:** `2026-05-11-design-log-convergence`
**Originating skill:** `/refine-design` (intake captured 13 tensions; resolution shapes annotated in `.ok-planner/design/tensions/`)

## Overview

The `/discover-design` pass that bootstrapped `.ok-planner/design/` produced a 46-concept catalog and a 39-entry tensions catalog. This spec converges the catalog: resolves 13 of those tensions, sharpens four concept files into new homes, drops four that don't earn their keep as standalone nouns, slims two whose scope was over-bundled, and extracts one small Go helper in `foundation/integration/` that the catalog's "single audited Abandon site" framing implicitly assumed.

12 of 13 resolutions are pure design-log mutations (concept files + tension lifecycle). One (`abandon-on-pass-duplicated-path`) adds a ~5-line helper extraction with a coordinated doc-language sweep across two concept files.

Net concept count: 46 → 46 (4 new + 4 dropped + 2 slimmed + 12 edited; the EDIT table has 14 rows because two concept files each receive two distinct edits). The catalog stays the same size but every constituent concept becomes sharper.

## Tensions resolved

| Tension slug | Resolution shape (short) | Primary file impact |
|---|---|---|
| `frame-stuck-dangling-adjacent` | Reword Adjacent line | `concepts/error-policy.md` |
| `claimant-guarded-backtick-noun` | Strip backticks | `concepts/lifecycle-handler.md` |
| `claim-producer-method-count-framing` | Unify on "4 verbs + Capabilities() handshake" | `concepts/claim-producer.md` + CLAUDE.md |
| `claim-vs-claim-handle-layer-annotation` | One-line layer annotation | `concepts/claim.md` + `concepts/claim-handle.md` |
| `transition-reason-missing-concept` | NEW concept | `concepts/transition-reason.md` |
| `on-event-handler-missing-concept` | NEW concept | `concepts/on-event-handler.md` |
| `licensing-boundary-fold-candidate` | Fold into module-layout; drop standalone | `concepts/module-layout.md`; drop `concepts/licensing-boundary.md` |
| `mcp-server-fold-into-control-api` | Fold into control-api; drop standalone | `concepts/control-api.md`; drop `concepts/mcp-server.md` |
| `scenario-harness-drop-from-catalog` | Drop standalone | drop `concepts/scenario-harness.md` |
| `userdata-overrides-fold-into-userdata` | Fold into userdata; drop standalone | `concepts/userdata.md`; drop `concepts/userdata-overrides.md` |
| `observability-split-cascade-graph-and-discovery-cache` | Promote 2 new; slim source | NEW `cascade-graph.md`, NEW `discovery-cache.md`; SLIM `observability.md` |
| `event-log-split-into-two` | Fold ledger into named-event; slim audit-log-only | `concepts/named-event.md`; SLIM `concepts/event-log.md` |
| `abandon-on-pass-duplicated-path` | Extract narrow helper + doc-language sweep | NEW `foundation/integration/abandon_claim.go`; `concepts/auto-terminal.md`; `concepts/terminal-resolution.md` |

Plus one tension superseded as a side effect: `events-table-name-overlap` (subsumed by `event-log-split-into-two`'s resolution).

## Concept catalog mutations

### NEW concepts (verbatim drafts)

#### `concepts/transition-reason.md`

```markdown
---
concept: transition-reason
status: as-is
aliases: []
references:
  - _discover/2026-05-10-state-machine-no-self-loop.md
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
---

# Transition reason

## What it is

`TransitionReason` is the audit-vocabulary enum carried on every node-state transition. Defined in `foundation/cascade/state.go:28-44` as a closed set of ~10 string constants (`ReasonHandlerComplete`, `ReasonHandlerError`, `ReasonPureCascade`, `ReasonInfraReenqueue`, `ReasonScheduleFire`, `ReasonAcquirePass`, etc.). Written by the state-transition apply path (`NextState` callers) and persisted into the audit event-log payload.

## Purpose

`TransitionReason` answers "why did the state machine move?" — for audit consumers. `last_outcome` answers the same question for the cascade-firing predicate. Same row, same transition, different vocabulary, different consumer. Splitting the vocabularies keeps the cascade gate one-column-simple while preserving rich audit detail.

## Boundaries

Owns: the closed enum, the write site at each state transition, the audit-event-log payload field carrying the reason. Does NOT own: dispatch eligibility (`node-state`), cascade-fire gate (`last-outcome`), event-log table mechanics (see `event-log` for audit-log mechanics). Adjacent: `node-state`, `last-outcome`, `cascade`, `event-log`.

## Invariants

- `ReasonHandlerError` is a deliberate dead-end sentinel: legal in audit but rejected as a transition reason by `NextState`.
- Reason values are exhaustively enumerated as Go constants; no free-form additions at runtime.
- Reason is written at every state transition; absence from the audit row is a defect.

## Aliases and historical names

None live. Pre-migration-004 code used `t.Changed` for the cascade-fire decision and a smaller reason vocabulary for audit; both surfaces were sharpened under the reactive-loops design (`.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`).

## Open within this concept

- `TransitionReason` (audit) and `last_outcome` (cascade-fire gate) carry overlapping but distinct vocabularies — see `tensions/transition-reason-vs-last-outcome.md`.
```

#### `concepts/on-event-handler.md`

```markdown
---
concept: on-event-handler
status: as-is
aliases: []
references:
  - _discover/reactive-loops-and-lifecycle-handlers.md
  - _discover/named-events-and-on-event-handlers.md
---

# On-event handler

## What it is

`on_event` is the fifth declarable handler surface on a node: a key-indexed map `{event_name → handler}` that dispatches per executor-emitted named event. Structurally distinct from the four single-slot lifecycle handlers (`on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored`), which each declare a single resolver. Shares the resolve+invalidate vocabulary (`resolve: pass | error`; `invalidate: {targets, frame}`) with the four lifecycle handlers.

## Purpose

Executors emit named events mid-run (`emit` is a non-terminal event with name + opaque payload). `on_event` lets templates react per event name — invalidating downstream nodes, transitioning the emitting node, or marking-as-passed — without coupling reactive policy to terminal-event handling.

## Boundaries

Owns: the map declaration in the template, the per-event-name resolver lookup at executor-emission time, the capabilities cross-check at template registration. Does NOT own: the named-event ledger storage (see `named-event`), the four single-slot lifecycle handlers (see `lifecycle-handler`), the discovery cache that powers the registration-time check (see `discovery-cache`). Adjacent: `lifecycle-handler`, `named-event`, `node`, `discovery-cache`.

## Invariants

- `on_event` is validated against `Capabilities.declared_events` at template registration when the peer is reachable via the observability handshake. The discovery cache supplies the declared-events list.
- Runtime treats unknown event names as no-ops if the peer was unreachable at registration (silent-skip; no error).
- Per-event-name handlers share the same resolve/invalidate vocabulary as the four lifecycle handlers; the per-emit `frame: in | next` discipline applies identically.

## Aliases and historical names

None live. The map shape was added under the reactive-loops design (`.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md`).

## Open within this concept

(no live tensions distinct from `lifecycle-handler` and `named-event`.)
```

#### `concepts/cascade-graph.md`

```markdown
---
concept: cascade-graph
status: as-is
aliases:
  - operator dashboard backplane
references:
  - _discover/observability-cascade-graph-endpoint.md
---

# Cascade graph

## What it is

The operator-dashboard HTTP-route backplane exposed by rimsky-control-api: `/observability/*`, `/events`, `/frames`, `/nodes/{instance}/{type}`, `/dispatches`. Routes mounted via `go-chi/chi`. Reads rimsky's own runtime state (frames, nodes, dispatches, events) and serves JSON to dashboards and operator tooling.

## Purpose

Operators (and dashboards built on top of rimsky) need to see what's running, what's wedged, what events have fired, and how cascade is propagating. `cascade-graph` is the read-only HTTP surface that exposes that state without coupling consumers to internal SQL or to the per-peer observability protocols.

## Boundaries

Owns: the route definitions, the per-route handlers, the JSON marshalling, the `inTx`-per-handler discipline. Does NOT own: per-peer executor/store observability protocols (see `observability`), audit-log writes (see `event-log`), control-plane mutation endpoints (see `control-api`). Adjacent: `observability`, `control-api`, `event-log`, `frame`, `node`.

## Invariants

- All cascade-graph HTTP handlers run inside a short fresh transaction (`inTx`).
- Read-only: no handler in this surface mutates state.
- Routes are mounted at bare paths (no `/v1/` prefix), matching the parent `control-api` versioning discipline.

## Aliases and historical names

The HTTP surface was previously documented inside `observability`; promoted to its own concept under the `2026-05-11-design-log-convergence` spec.

## Open within this concept

(no live tensions.)
```

#### `concepts/discovery-cache.md`

```markdown
---
concept: discovery-cache
status: as-is
aliases:
  - capabilities cache
references:
  - _discover/observability-handshake-discovery-cache.md
---

# Discovery cache

## What it is

An in-memory per-peer `Capabilities` cache populated by the observability handshake at startup. Lives in `modeling/observability/discovery.go` (and the handshake fill path in `handshake.go`). Indexed by peer name; entry shape includes the peer's `declared_events`, observability-protocol availability, and a status enum (`Reachable | Unreachable`).

## Purpose

The capabilities each peer declares are needed at template registration (for the `on_event` declared-events cross-check) and at runtime fallback decisions (unknown event names treated as no-ops if peer was unreachable at registration). Probing peers synchronously at every check would couple registration latency to peer availability. The discovery cache decouples them: probe at startup, cache, refresh on a loop, check against cache.

## Boundaries

Owns: the in-memory cache structure, the per-peer entry shape, the registration-time consult path, the reachability status. Does NOT own: the handshake invocation (see `observability`), the executor/store observability protocols themselves (see `observability`), the runtime unknown-event-as-no-op fallback (see `on-event-handler`). Adjacent: `observability`, `on-event-handler`, `executor`, `claim-producer`.

## Invariants

- Best-effort fill: unreachable peers are recorded as `Unreachable` and never abort startup.
- Reads are eventually-consistent; the refresh loop updates entries on its own cadence.
- The cache is in-memory only; restart resets state to a fresh handshake pass.

## Aliases and historical names

The cache and its handshake population were previously documented inside `observability`; promoted to its own concept under the `2026-05-11-design-log-convergence` spec.

## Open within this concept

(no live tensions.)
```

### SLIM concepts (verbatim rewrites)

#### `concepts/observability.md` (post-slim)

Slim removes the cascade-graph HTTP routes and the discovery-cache material (both promoted to their own concepts). Final content:

```markdown
---
concept: observability
status: as-is
aliases: []
references:
  - _discover/2026-05-10-observability-optional-protocols.md
---

# Observability

## What it is

The peer-facing optional observability protocols and the startup handshake that probes them. Two optional gRPC protocols per peer (`ExecutorObservability`, `StoreObservability`) exposing `Capabilities` / `GetTrace` / `StreamTrace`. The handshake (`modeling/observability/handshake.go`) probes each declared peer in parallel at rimsky startup, populating the `discovery-cache`. Also the canonical site for the per-peer `userdata_schema` declaration (read from the handshake, applied at template registration and at dispatch post-merge/post-substitution).

## Purpose

Peers declare their own capabilities and trace surfaces; rimsky should learn them once, cache the result, and consult the cache at validation gates. Keeping the protocol-side concept separate from the cache it populates (`discovery-cache`) and the operator-dashboard backplane (`cascade-graph`) keeps each concept's boundary sharp.

## Boundaries

Owns: the optional peer protocols, the handshake mechanism, the refresh-loop policy, the per-peer `userdata_schema` validation surface. Does NOT own: the cache the handshake populates (see `discovery-cache`), the operator-dashboard HTTP routes (see `cascade-graph`), the per-event audit log (see `event-log`). Adjacent: `discovery-cache`, `cascade-graph`, `executor`, `claim-producer`, `event-log`, `named-event`.

## Invariants

- The handshake is best-effort: unreachable peers recorded as `Unreachable` in `discovery-cache`; never aborts startup.
- Per-peer `userdata_schema` validates at template registration AND at dispatch post-merge/post-substitution.

## Aliases and historical names

Pre-`2026-05-11-design-log-convergence`, this concept also covered the cascade-graph HTTP routes and the discovery cache; those are now `cascade-graph` and `discovery-cache` respectively.

## Open within this concept

- `userdata_schema` placement on the observability protocol (read by rimsky to validate userdata bytes at template-registration and dispatch time) sits in tension with `@blessed-invariant 11` opacity — see `tensions/userdata-schema-as-opacity-exception.md`.
```

#### `concepts/event-log.md` (post-slim to audit-log-only)

Slim removes the `rimsky_node_events` named-event ledger material (folded into `named-event` as a "Ledger storage" subsection). Filename and slug retained per refine-design step 5 sub-decision (option C). Final content:

```markdown
---
concept: event-log
status: as-is
aliases:
  - audit log
  - rimsky_events table
references:
  - _discover/2026-05-10-event-log-append-only-jsonb.md
---

# Event log (audit log)

## What it is

`rimsky_events` — rimsky's internal append-only audit log. Schema: `id BIGSERIAL`, `instance_id`, `node_id`, `kind TEXT` (free-form, no enum CHECK), `payload JSONB`, `occurred_at TIMESTAMPTZ`. Indexed by `(node_id, occurred_at DESC)`, `(instance_id, ...)`, `(kind, ...)`. Written by rimsky's supervisor / scheduler / control-api at observable transitions. Read by the `/events` route in `cascade-graph` for the operator dashboard.

## Purpose

Rimsky needs an append-only record of "what happened" for incident review, operator dashboards, and debugging — a record rimsky owns (rimsky-readable JSONB, not bound by `@blessed-invariant 21` opacity). The free-form `kind` column lets new event categories appear with zero migration; the price is that typos produce events no consumer finds.

## Boundaries

Owns: the `rimsky_events` schema, the CRUD path, the read pattern feeding `cascade-graph`. Does NOT own: the named-event ledger (`rimsky_node_events` — see `named-event` "Ledger storage" subsection), retention policy (operator-managed), interpretation of individual `kind` strings (lives in consumers). Adjacent: `cascade-graph` (reads from `/events`), `observability`, `named-event` (sibling append-only table with different opacity discipline).

## Invariants

- `rimsky_events.kind` is free-form; no enum CHECK. Zero-migration to add a new kind; typos produce events no consumer finds.
- `rimsky_events.payload` is rimsky's own JSONB — readable by rimsky for the dashboard and audit consumers. NOT bound by `@blessed-invariant 21` (which governs the named-event ledger).
- No built-in retention; operator-managed retention is required.

## Aliases and historical names

Pre-`2026-05-11-design-log-convergence`, this concept also covered `rimsky_node_events` (named-event ledger). That material moved to `concepts/named-event.md` "Ledger storage" subsection. Filename `event-log.md` retained; content is now audit-log-only.

## Open within this concept

- `rimsky_events.kind` is free-form — see `tensions/events-kind-no-enum.md`.
```

### DROP concepts (with target subsection drafts where applicable)

#### Drop `concepts/licensing-boundary.md` → fold into `concepts/module-layout.md`

Append to `module-layout.md`:

```markdown
## Licensing boundary

Per-directory Apache-2.0-vs-AGPL-3.0 mapping in `licensing.yml`, enforced by `rimsky-license-check` with longest-prefix-match-wins. Apache surface covers protocols, foundation, modeling (excluding `eval/`), CLI binaries; AGPL surface covers `modeling/qualityrule/eval/` and any directories explicitly mapped under AGPL. Repo-organization concern; not a runtime noun. The check is build-step enforcement, not runtime.

(Adjacent: previously documented as a standalone concept; folded here under `2026-05-11-design-log-convergence`.)
```

#### Drop `concepts/mcp-server.md` → fold into `concepts/control-api.md`

Append to `control-api.md`:

```markdown
## Agentic MCP shim

The standalone Go module under `mcp-servers/control-api/` wraps the HTTP control-api surface as MCP (Model Context Protocol) tools. Implements `initialize` / `tools/list` / `tools/call` over `POST /mcp` using stdlib `encoding/json` + `go-chi/chi` (no third-party MCP SDK). Catalog covers templates, tags, instances, nodes, diagnostics. Strict pass-through: no validation, no caching, no synthesis. Forwards `Authorization: Bearer <CONTROL_API_TOKEN>` to the underlying control-api. Independent Go module with no runtime dependency on modeling/foundation; catalog is hand-curated in `tools.go`.

Note: `executors/claude-agent/` embeds a separate per-run *internal* MCP server (`internal-mcp-server.ts`) — same protocol, different role (per-dispatch executor-local tools vs operator control-plane). The dual-MCP-role observation is part of this subsection; do not confuse the two.

(Previously documented as a standalone concept `mcp-server`; folded here under `2026-05-11-design-log-convergence`.)
```

#### Drop `concepts/scenario-harness.md` (no fold)

No target subsection. The harness remains documented in CLAUDE.md "Build & test." Update `concepts/conformance.md` Adjacent block to remove `scenario-harness` (or reword to "in-repo scenario tests under `test/scenarios/` use `modeling/scenario.Start` as their bring-up").

#### Drop `concepts/userdata-overrides.md` → fold into `concepts/userdata.md`

Append to `userdata.md`:

```markdown
## Per-instance overrides

Templates declare a per-node userdata blob; some operations (tracing, synthetic-blocker scenarios, ad-hoc tuning, per-run artifacts) need to alter that blob for one instance without forking the template. Per-instance overrides handle that.

Shape: `{by_executor: {<executor-name>: {...}}, by_node: {<node-name>: {...}}}`. Stored on `rimsky_instances.userdata_overrides`. Deep-merged at dispatch time in the order `template → by_executor[<executor>] → by_node[<node>]` (more specific wins). Merge helper is `modeling/shared.DeepMergeJSON`.

Validation discipline (preserves `@blessed-invariant 11`):
- Inspects only routing keys (`by_executor`, `by_node`, plus the executor/node names which must be declared in the template). Fragment values never inspected.
- Unknown top-level keys are rejected at create-time.
- Nodes whose `executor_name` is null (claim-only path) get only `by_node[name]` overrides.

(Previously documented as a standalone concept `userdata-overrides`; folded here under `2026-05-11-design-log-convergence`. Added under the platform-extensions design 2026-05-08.)
```

### EDIT concepts (trivial rewords; see tension files for shape)

| File | Change | Tension |
|---|---|---|
| `concepts/error-policy.md` | Drop `frame-stuck` from `Adjacent:` block; point at `frame` instead | `frame-stuck-dangling-adjacent` |
| `concepts/lifecycle-handler.md` | Strip backticks around `claimant-guarded`; reword to "the claimant-guarded release discipline (`@blessed-invariant 4`)" | `claimant-guarded-backtick-noun` |
| `concepts/lifecycle-handler.md` | Update `Adjacent:` line to point at the new `on-event-handler` concept | `on-event-handler-missing-concept` |
| `concepts/named-event.md` | Update `Adjacent:` line to point at the new `on-event-handler` concept | `on-event-handler-missing-concept` |
| `concepts/named-event.md` | Add "Ledger storage" subsection covering the `rimsky_node_events` material moved from `event-log.md` | `event-log-split-into-two` |
| `concepts/node.md` | Update `Adjacent:` line to point at the new `on-event-handler` concept | `on-event-handler-missing-concept` |
| `concepts/node-state.md` | Update Boundaries to cite the new `transition-reason` concept by slug instead of inline mention; update `Adjacent:` to add `transition-reason` | `transition-reason-missing-concept` |
| `concepts/claim-producer.md` | Reword BOTH the "What it is" section (currently lists "5 methods: `Open`, `Commit`, `Abandon`, `Release`, `Capabilities`" — replace with "4 verbs (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake") AND the Invariants block (currently "The five-method protocol plus `Capabilities()` startup handshake" — replace with "The 4-verb protocol plus the `Capabilities()` startup handshake"). Both call sites must be updated for the unification to be coherent. | `claim-producer-method-count-framing` |
| `concepts/claim.md` | Add a one-line layer annotation at the top of the Definition: "`claim` is the protocol-layer noun returned by `ClaimProducer.Open`; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing. They have different invariants by layer." | `claim-vs-claim-handle-layer-annotation` |
| `concepts/claim-handle.md` | Add the matching one-line layer annotation at the top of the Definition (same text, mirrored) | `claim-vs-claim-handle-layer-annotation` |
| `concepts/executor.md` (optional) | Adjacent block: optionally add note that claude-agent embeds an internal MCP server (now documented under `control-api`'s Agentic MCP shim subsection) | `mcp-server-fold-into-control-api` |
| `concepts/conformance.md` | Remove `scenario-harness` from `Adjacent:` block, or reword to point at `test/scenarios/` directly | `scenario-harness-drop-from-catalog` |
| `concepts/auto-terminal.md` | Reword Invariant 5 to qualify the `OnAcquireUnavailable` carve-out — see tension's picked-shape annotation | `abandon-on-pass-duplicated-path` |
| `concepts/terminal-resolution.md` | Reword opening prose to consistently describe the post-dispatch routing through `ResolveClaimHandleTerminal` and the pre-dispatch carve-out via the new shared helper | `abandon-on-pass-duplicated-path` |

Also: any concept whose `Adjacent:` block references a dropped concept (`licensing-boundary`, `mcp-server`, `scenario-harness`, `userdata-overrides`) gets that reference updated to point at the fold-destination (or removed if no fold-destination). Discovery: grep `Adjacent:` references across all concept files during execute-plan.

## Code refactor: `abandon-already-opened-claim` helper

### New file: `foundation/integration/abandon_claim.go`

Narrow helper centralising the `producer.Abandon` call on an already-Open'd claim. Both the pre-dispatch partial-Abandon path and the post-dispatch unified-engine Abandon branch call through this helper.

```go
// abandonOpenedClaim fires Producer.Abandon on a claim whose Open already
// succeeded. Centralizes claim_id construction and is the single audited
// site for any future audit-emit / telemetry on producer-Abandon-on-
// opened-claim. Distinct from ResolveClaimHandleTerminal: this helper
// does NOT touch the rimsky_claim_handle row (the post-dispatch caller
// owns that delete in its tx; the pre-dispatch caller has no row to
// delete because the acquisition tx rolled back before this helper runs).
func abandonOpenedClaim(
    ctx context.Context,
    producer locks.ClaimProducer,
    claimHandleID shared.UUID,
    scope, address []byte,
) error {
    claimID := locks.ClaimID(claimHandleID.String())
    return producer.Abandon(ctx, claimID, scope, address)
}
```

Scope and address are `[]byte` to match the existing types in the codebase: `TerminalDecision.Scope/Address` are declared as `[]byte` (`foundation/integration/terminal_decision.go:91-92`), and the `ClaimProducer.Abandon` interface signature is `Abandon(ctx, claimID, scope []byte, address []byte) error` (`protocols/claimproducer/claimproducer.go:60`). They remain opaque per `@blessed-invariant 20` regardless of the static type used to declare them — `json.RawMessage` is its own type alias for `[]byte`, but the in-tree pattern is the underlying `[]byte`.

### Call sites updated

- **`foundation/integration/runner_lifecycle.go::abandonPartialLocks`** (~line 67-80): replace the direct `lk.Store.Abandon(...)` call with `abandonOpenedClaim(ctx, lk.Store, lk.ClaimHandleID, scope, address)`. Continue to log a warning on failure (this site is post-rollback, fail-soft).
- **`foundation/integration/terminal_decision.go::ResolveClaimHandleTerminal`** Abandon branch only (~line 123): replace `verbErr = td.Producer.Abandon(ctx, claimID, td.Scope, td.Address)` with `verbErr = abandonOpenedClaim(ctx, td.Producer, td.ClaimHandleID, td.Scope, td.Address)`. The `claimID := locks.ClaimID(td.ClaimHandleID.String())` construction at line 117 is shared with the Commit branch (line 121) and must remain — only the Abandon-branch RPC call is replaced. The Commit branch (`td.Producer.Commit(...)`) is unchanged. The surrounding `ClaimHandles.Delete(ctx, td.ClaimHandleID, td.SupervisorID, tx)` claimant-guarded delete stays in `ResolveClaimHandleTerminal`'s flow unchanged.

### Doc-language sweep (paired with code refactor; lands in same change)

- `concepts/auto-terminal.md` Invariant 5: reword to qualify the carve-out. Suggested wording: "Unified `ResolveClaimHandleTerminal` is the audited post-dispatch entry point for orphan-reaper bail paths and error-policy `pass`/`error` resolutions on already-Open'd claims. The pre-dispatch `OnAcquireUnavailable` `pass`/`error` carve-out routes through the shared `abandonOpenedClaim` helper instead — the rimsky_claim_handle rows are already gone (rolled back) by the time it fires, so the unified engine's delete step has nothing to do."
- `concepts/terminal-resolution.md` opening prose (`OnAcquireUnavailable` paragraph): reword to consistently describe pre-dispatch routing through `abandonOpenedClaim` rather than "a direct producer call." Match the new helper-extracted shape.

### Tension body reword (post-extraction)

- `tensions/abandon-on-pass-duplicated-path.md` "What is muddy" — reword to correctly identify the genuinely duplicated path (pre-dispatch `handleAcquireUnavailable.abandonPartialLocks` only) before the helper extraction landed; preserve as historical breadcrumb when the tension moves to `_resolved/`.

## CLAUDE.md updates

- **"4 verbs + Capabilities()" framing sweep**: search CLAUDE.md for "5 methods" / "five methods" / "5-method" / "five-method" wording on the ClaimProducer protocol. Unify on "4 verbs (`Open` / `Commit` / `Abandon` / `Release`) plus the `Capabilities()` startup handshake" everywhere. (Likely small; CLAUDE.md "What this repo is" already uses the 4-verbs framing.)
- **References to dropped concepts**: grep CLAUDE.md for `mcp-server`, `licensing-boundary`, `scenario-harness`, `userdata-overrides` and update wording to point at the new homes where the references are load-bearing (e.g. CLAUDE.md "Reference deployment" mentions the MCP server — reword to reference the agentic MCP shim under `control-api`).
- **No new gotchas or blessed invariants added** by this spec. The helper extraction does not change runtime semantics; it just centralizes the verb call.

## Testing strategy

### Regression guarantee

After the refactor, the following must continue to pass with no test edits:

- `go test ./foundation/integration/... -count=1` (covers `runner_lifecycle.go` and `terminal_decision.go` unit tests, including `auto_terminal_test.go` and `on_error_test.go`)
- `go test ./test/scenarios/... -count=1` (covers `OnAcquireUnavailable` pass/error scenarios and `releaseLocksInTx` scenarios)
- `make test-all` (full module suite)

If any existing test edits are required, that's a signal the refactor changed runtime semantics — reviewers should treat that as a defect to investigate.

### New tests (minimal)

- `foundation/integration/abandon_claim_test.go` — a small unit test for `abandonOpenedClaim` verifying it forwards args correctly (`claim_id` constructed from `claim_handle_id.String()`, scope/address passed through opaque). Two table cases: success path and producer-Abandon-error pass-through. ~30 lines.
- **No new scenario test**. The existing scenario coverage (`OnAcquireUnavailable` pass/error in `test/scenarios/`, releaseLocksInTx in `test/scenarios/`) already exercises both call sites with realistic claim-producer peers; the refactor is a pure internal restructure of the verb-call site.

### Race / flake coverage

- The race-sensitive paths (queue, supervisor, scheduler) are not modified by this spec. No `-race -count=N` flake-hunt required beyond what the existing CI runs.

## Order of operations

The design-log mutations (12 of 13 resolutions) are independent of each other: they can land in any order. The code refactor + paired doc-language sweep (the 13th resolution) must land together.

Suggested execute-plan sequencing:

1. **Code refactor + paired doc sweep** (single change-set): add `abandon_claim.go`, update the two call sites, reword `auto-terminal.md` Invariant 5 and `terminal-resolution.md` opening prose, reword `tensions/abandon-on-pass-duplicated-path.md` body. Move tension to `_resolved/` after lint/test pass.
2. **NEW concepts** (4 files): write `transition-reason.md`, `on-event-handler.md`, `cascade-graph.md`, `discovery-cache.md`. Move respective tensions to `_resolved/`.
3. **SLIM concepts** (2 files): rewrite `observability.md` (post-promote of cascade-graph + discovery-cache) and `event-log.md` (post-fold of named-event ledger). Update `concepts/named-event.md` with the "Ledger storage" subsection. Move respective tensions to `_resolved/` (including the superseded `events-table-name-overlap.md`).
4. **DROP concepts** (4 files): land the fold-destination subsections in `module-layout.md`, `control-api.md`, `userdata.md`. Update `conformance.md` Adjacent. Delete the four standalone files. Move respective tensions to `_resolved/`.
5. **EDIT concepts** (the trivial rewords): batch the small edits in `error-policy.md`, `lifecycle-handler.md`, `claim-producer.md`, `claim.md`, `claim-handle.md`, `named-event.md`, `node.md`, `node-state.md`. Update any `Adjacent:` blocks referencing dropped concepts. Move respective tensions to `_resolved/`.
6. **CLAUDE.md sweep**: 4-verbs+Capabilities framing; references to dropped concepts.
7. **Regenerate `concepts.md`** TOC at the end (`discover-design`'s regeneration step, also run by `execute-plan` after concept mutations).

The 7 steps are mostly independent; only step 1 must be atomic. Steps 2–6 can be interleaved as the executor finds convenient.

## Out of scope

- **`Store = ClaimProducer` alias retirement**: still an open tension (`store-vs-claim-producer-vocabulary.md`); not addressed by this spec.
- **YAML `stores:` legacy alias**: still an open tension (`yaml-stores-alias.md`); not addressed.
- **`transition-reason` vs `last-outcome` vocabulary reconciliation**: the new `transition-reason` concept creates a home for the audit vocabulary; the relationship-tension `transition-reason-vs-last-outcome.md` remains open. Reconciliation belongs in a separate refine-design session.
- **`compose:` prefix server-side enforcement**: separate open tension.
- **`pre-v1-hash-instability`**: separate open tension.
- **All other open tensions** not listed in the 13 resolving in this spec.
- **Adding `@concept:` annotations across the codebase**: that's a separate brainstorm if the project formally adopts the annotation convention; not part of this spec. (Execute-plan may incidentally leave annotations at sites it touches, per the annotate-on-consult rule.)

## Acceptance criteria

- All 13 resolving tensions are moved to `.ok-planner/design/tensions/_resolved/` with a `status: resolved` frontmatter and a `resolution:` block summarizing the outcome. The superseded `events-table-name-overlap.md` is also moved to `_resolved/` with a resolution pointing at `event-log-split-into-two`.
- `.ok-planner/design/concepts.md` TOC is regenerated and reflects: 4 new concepts (`transition-reason`, `on-event-handler`, `cascade-graph`, `discovery-cache`), 4 dropped concepts (`licensing-boundary`, `mcp-server`, `scenario-harness`, `userdata-overrides`), 2 slimmed concepts (`observability`, `event-log`). Catalog count remains 46.
- `foundation/integration/abandon_claim.go` exists with the documented helper. Both call sites use it.
- `make build-all`, `make test-all`, `make lint` all pass on the post-change tree.
- `go test ./foundation/integration/... ./test/scenarios/... -count=1` passes with no test edits required (existing coverage suffices for the helper extraction).
- CLAUDE.md "4 verbs + Capabilities" sweep complete; no surviving references to dropped concept slugs.
- All concept files' `Adjacent:` blocks are internally consistent (no references to dropped concepts; new concepts cross-linked from their natural neighbors).
