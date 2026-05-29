# Divergences — 2026-05-28-quality-of-life-features

Audit of the working tree (all work staged) against the literal text of
`plan:2026-05-28-quality-of-life-features` and
`spec:2026-05-28-quality-of-life-features-design`. The implementation
follows the plan closely; the items below are the choices a reviewer
would want to know about. Touched Go packages build clean
(`go build ./lib/control/controlapi/... ./lib/foundation/cascade/... ./cmd/rimsky/...`).

---

## 1. New MCP tools `template_validate` / `instance_kill` lack `builtinSchemas` entries (undocumented gap)

- **What the plan said:** Tasks 1 and 8 add the `MCPTools` names
  `template_validate` and `instance_kill` to `v1Actions`. Neither plan
  nor spec mentions `builtinSchemas`.
- **What was implemented:** Both names landed in `v1Actions`
  (`code:lib/control/controlapi/actions.go::v1Actions`), which makes them
  appear in the MCP `tools/list` catalog and routable via `tools/call`
  (the catalog re-enters the chi router, so the tools are functionally
  live). But neither tool was added to
  `code:lib/control/controlapi/mcp_route.go::builtinSchemas`, whose
  doc-comment states it must be kept "in lockstep with `v1Actions`" and
  that "write tools need explicit shapes so the client can validate
  before round-tripping." `instance_kill` is a write tool (it takes
  `idOrKey` from the route plus an optional `reason` body) and now falls
  back to the generic `{"type":"object"}` catalog schema; `template_validate`
  (which takes a `{spec}` body) likewise falls back. No test guards the
  lockstep, so the gap is silent.
- **Inferred reason:** Plan blind spot. The plan's cross-cutting
  "action-registry discipline" section enumerated the three coordinated
  edits (route, `v1Actions`, `MCPTools`) but never surfaced
  `builtinSchemas` as a fourth coordinated edit, so the implementer was
  never directed there. The self-report ("added a `template_validate`
  MCP tool *name* … without wiring an actual MCP tool surface") gestures
  at this; the precise shape is that *both* new tools are missing
  `builtinSchemas` shapes.

## 2. `instance_killed` concept-doc text claims broader state coverage than the code implements

- **What the plan said:** Task 7 explicitly narrows the force-fail to
  resource-holding `running`/`parked` node-runs and says to add only
  those two `NextState` arms — narrower than spec line ~90, which lists
  `running`, `stale`, `parked`, `fresh`. Task 27 prescribes the
  `transition-reason.md` wording ("accepted by the next-state function
  for each non-terminal current state").
- **What was implemented:** The code is correctly narrow — only
  `running → failed` and `parked → failed` arms exist
  (`code:lib/foundation/cascade/state.go::NextState`). But the durable
  concept doc text that landed
  (`file:.ok-planner/design/concepts/transition-reason.md`) reads
  "`instance_killed` … drives **any** non-terminal node state to failed
  and is accepted by the next-state function for **each** non-terminal
  current state." `fresh` and `stale` are non-terminal but are NOT
  accepted arms, so the concept doc overstates the shipped state machine.
- **Inferred reason:** Spec-prose-vs-code tension the implementer
  resolved in the code (per Task 7) but carried the spec's broader wording
  verbatim into the concept doc (per Task 27, which prescribed that exact
  text). The plan's two tasks are internally inconsistent on this point;
  the code followed Task 7, the doc followed Task 27. Self-reported for
  the spec line, but the propagation into the durable doc was not called out.

## 3. `watch` feed is source-grouped per poll cycle, not chronologically interleaved

- **What the plan said:** Task 18 / spec Feature 4 — "interleaving three
  poll sources into one chronological feed" and "Interleaves three poll
  sources into one chronological feed."
- **What was implemented:** `code:cmd/rimsky/cli/watch.go::RunWatch`
  drains all new events, then all new breakpoint hits, then checks the
  terminal flag, each poll cycle — the three sources are emitted
  source-grouped within an iteration, not merged into a single
  timestamp-ordered stream. Within each source the order is correct
  (event high-watermark cursor; hit seq watermark).
- **Inferred reason:** Cleaner/simpler shape. A true chronological merge
  would require buffering and sorting across heterogeneous cursors
  (event id vs hit seq) with no shared clock; the per-cycle grouping is
  the pragmatic reading and matches the `RunInstanceEvents` cursor
  pattern the plan told the implementer to reuse.

## 4. `watch` event lines use the event's native `Kind`, not the plan's named line types

- **What the plan said:** Task 18 / Feature 4 — "Prints `frame.start` /
  node-termination / `breakpoint.hit` / terminal lines."
- **What was implemented:** `code:cmd/rimsky/cli/watch.go::printWatchEvent`
  labels each event line with the event's own free-text `Kind`
  (`work_started`, etc.), not the literal `frame.start` /
  node-termination kinds the plan named. Breakpoint-hit and terminal
  lines are labeled as the plan said.
- **Inferred reason:** Plan error — the codebase's event kinds are
  free-text (`tension:events-kind-no-enum`), so `frame.start` /
  node-termination are not stable literal kinds to filter on. The
  implementer surfaced every event under its native kind, documented in a
  code comment. Self-reported.

## 5. Force-terminate claim abandonment is self-assembled from `ListByHolderNode` + `Promote`

- **What the plan said:** Task 9 / spec Feature 2 — abandon the
  force-failed node-runs' "in-flight (uncommitted) claim handles", do
  NOT call `runtime.ReleaseHeldDurableClaims`, and "find the
  uncommitted-claim accessor on `Persist.ClaimHandles()` (grep the
  interface for a per-node-run / per-instance claim lister filtered to a
  non-committed state)."
- **What was implemented:** `code:lib/control/controlapi/instances.go::abandonInFlightClaims`
  lists per-node via `ClaimHandles().ListByHolderNode`, filters in Go to
  `active`-state rows with a non-nil `HolderSupervisorID`, and promotes
  each to `abandoned` via the claimant-guarded `Promote`. There is no
  single "uncommitted-claim lister" accessor; the implementer assembled
  the behavior from the per-node lister plus the guarded promote, and
  scoped it to `active` rows (committed-durable rows are untouched, per
  the plan). `handleDeleteInstance` by contrast uses the higher-level
  `runtime.ReleaseHeldDurableClaims`.
- **Inferred reason:** Forced choice — no ready-made helper exists for
  abandoning a node-run's uncommitted in-flight claims, so the implementer
  composed the guarded primitives. Sound and consistent with the plan's
  explicit "do NOT use ReleaseHeldDurableClaims" constraint.

## 6. Shared `instanceRedact` helper + `handleGetInstance` rewire (extra, unplanned)

- **What the plan said:** Task 9 referenced `handleGetInstance` /
  `toInstanceItem` as cousins for the redact pattern but did not call for
  extracting a shared helper.
- **What was implemented:** The per-hash `ParamsRedact` load was extracted
  into `code:lib/control/controlapi/instances.go::instanceRedact`, and
  `handleGetInstance` was rewired onto it so terminate and GET share one
  redaction path.
- **Inferred reason:** Cleaner shape — avoids duplicating the best-effort
  template-load-for-redaction block across the two single-instance
  projection handlers. Self-reported.

## 7. Unplanned `findingToProjection` helper with a path fallback

- **What the plan said:** Task 2 — merge findings inline: static
  `res.Errors` via `e.Path`/`e.Msg` plus pipeline `outcome.Errors`/`Warnings`,
  each projected to `{path, msg}`.
- **What was implemented:** `code:lib/control/controlapi/templates.go::handleValidateTemplate`
  projects static errors inline as the plan said, but pipeline findings go
  through a new helper `findingToProjection` that adds a fallback: when a
  pipeline `ValidationFinding.Path` is empty, it substitutes
  `ServiceName` (plus `Role` in parens) so an operator still sees the
  finding's origin.
- **Inferred reason:** Cleaner/more-useful shape. The pipeline
  `ValidationFinding` carries `ServiceName`/`Role` fields the static
  `node.ValidationError` lacks; the helper preserves that provenance
  rather than emitting blank-path findings. A small, sensible enrichment
  beyond the plan's literal mapping.

## 8. `template lint` prints findings to stderr, "ok" to stdout (stream choice unspecified)

- **What the plan said:** Task 5 — "Print `validation_warnings` then
  `validation_errors` (path + msg) in human mode." Stream not specified.
- **What was implemented:** `code:cmd/rimsky/cli/templates.go::RunTemplateLint`
  writes warnings and errors to **stderr** and prints a bare `ok` to
  **stdout** when clean. JSON mode emits the whole `ValidateResult` to
  stdout as planned.
- **Inferred reason:** Linter-convention choice — keeps stdout clean for
  scripting (the exit code carries the verdict; diagnostics go to stderr).
  Reasonable and unobjectionable; noted only because the plan left the
  stream implicit.

## 9. Spec testing-strategy item dropped: no scenario test for the await_async-stuck case

- **What the spec said:** Testing strategy for Feature 2 — "A scenario
  test for the await_async-stuck case: a node parked in `running` on an
  async ack is freed by terminate."
- **What was implemented:** `code:lib/control/controlapi/instances_test.go`
  covers terminate against a directly-seeded `running` node-run (which is
  structurally the await_async-stuck state — still `running`, holding its
  claim) plus the no-body, idempotent, and not-found cases. No separate
  test under `test/scenarios/` drives a real await_async park.
- **Inferred reason:** Judged redundant. Plan Task 10 (the executable
  spec for this pass) did not require a scenario test; it only listed the
  handler-level cases the implementer wrote. The await_async path's
  terminal state is `running`, which the seeded-`running` test already
  exercises. The spec's scenario-test line was an aspiration the plan did
  not carry forward.

---

## Verified-and-faithful (self-reported items that matched the diff, no divergence)

- Pass 2 narrowing to `running`/`parked`-only `NextState` arms — matches
  the plan exactly (the broader-spec tension is captured in finding #2 as
  it relates to the concept doc, not the code).
- Pass 3 clitest fake gained a `POST /instances/{idOrKey}/terminate`
  route + `IsTerminated` state helper.
- Pass 5 clitest fake gained the breakpoint-hits route +
  `AddBreakpointHit`/`BreakpointHitsFor` state methods, and the
  `captureStdout` test helper landed.
- Pass 6 attached `emittedEvents` to `AgentOutcome` via an
  `AgentOutcomeBase` intersection and a shared `emittedEventsCallbackSlot`
  helper (both `outcomeToCallbackBody` variants now exported for tests),
  rather than changing the callback-body signature.
- Pass 6 `emit_named_event` auto-joins the CLI `REQUIRED_CALLBACK_TOOLS`
  allowlist (derived from `TOOL_DEFINITIONS`), guarded by a test.
- `watch` placed in a new `cmd/rimsky/cli/watch.go` — the plan explicitly
  permitted "or a new `cli/watch.go`".
- Breakpoint-hits route reuses in-package `hitToWireShape` / `parseSinceLimit`
  with no extraction — matches Task 14 ("reuse them directly — no
  extraction needed"), even though spec Feature 3 loosely said "extract to
  a small shared helper." Plan was followed.
- The executor test suite is `vitest`, not `jest` as CLAUDE.md/plan prose
  loosely says — pre-existing fact about the test runner, not a change
  introduced here.
- The `EmitNamedEventInput` zod schema in `internal-mcp-tools.ts` is not
  imported by the server handler (which defines an inline schema) — this
  matches the existing pattern for every sibling `*Input` schema, so it is
  not dead/divergent code.
