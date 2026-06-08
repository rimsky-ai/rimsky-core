# Divergence record — 2026-06-06-comprehensive-gap-closure

Audit of the working tree against plan
`.ok-planner/plans/2026-06-06-comprehensive-gap-closure.md` and spec
`.ok-planner/specs/2026-06-06-comprehensive-gap-closure-design.md`
(43 user-outcome stories turned into ~73 RED→GREEN passes plus a final
Acceptance section of one end-to-end gate per story).

## How this audit was produced (and why it replaces the prior file)

**This file fully replaces a predecessor whose entire undershoot section was
false on the current tree.** The predecessor (and the predecessor it claimed to
correct) asserted that the plan's final-Acceptance end-to-end gate files — the
named full-stack-boot scenario tests for the CLICTRL onboarding stories, the
claim-scope-directive story, and the two cascade stories — "do not exist" / were
"never authored." **Every one of those files exists on the tree as it now stands
and is a substantive full-stack test, not a stub.** Verified by direct `os.Stat`
of each named path:

| Plan gate file | Status now |
|---|---|
| `lib/services/test/scenarios/cli_example_spec_e2e_test.go` | EXISTS — drives real `cli.RunRun` against a live all-in-one stack |
| `lib/services/test/scenarios/cli_compose_up_down_e2e_test.go` | EXISTS |
| `lib/services/test/scenarios/cli_watch_chronological_e2e_test.go` | EXISTS |
| `lib/services/test/scenarios/control_api_idempotency_required_e2e_test.go` | EXISTS |
| `lib/services/test/scenarios/control_api_compose_prefix_guard_e2e_test.go` | EXISTS |
| `lib/services/test/scenarios/control_api_node_signal_type_e2e_test.go` | EXISTS |
| `test/scenarios/stores/claim_scope_directive_e2e_test.go` | EXISTS — `TestAcceptance_ClaimScopeEndToEnd` |
| `test/scenarios/cascade_operator_frame_in_e2e_test.go` | EXISTS — real control-api + frame engine |
| `test/scenarios/cascade_waitset_topic_taxonomy_e2e_test.go` | EXISTS |

`TestCLIExampleSpec_RunReachesTerminal` (the story the predecessor singled out as
"not exercised at all") boots the real stack, drives the shipped
`examples/compose/template-a.yml` through the real `rimsky run` verb in-process,
asserts the printed `instance_id=<uuid>`, reaches `instance get`, polls the node
to terminal `fresh`, and then re-runs the README-documented invocation. The
`examples/README.md` documents `rimsky run examples/compose/template-a.yml` in a
fenced block; the test runs it as written. The predecessor's "U1/U2/U3" and its
"Divergence 9" are all stale-snapshot artifacts and are dropped.

**State of the tree at audit time.** Everything is staged; `git diff` (unstaged)
is empty; there are no untracked files. All four workspace modules build clean
(`go build ./...` exit 0 in root, `lib/foundation`, `lib/protocols`,
`lib/services`, and `examples/`). Story implementations were sampled site-by-site
and are real (forwarders, wired evaluators, real migrations, real e2e tests) —
not stubs.

---

## Undershoot — completion failures (must not ship)

**None.** Every spec story's value-delivering component is real and the named
acceptance gate exists. Site-by-site verification of the surfaces the spec called
out as stubs / declarations-without-emit found a genuine implementation at each:

- The three proxy protocols the spec named as `Unimplemented` stubs
  (`publisher`/`validation`/`data-processing`) are now real forwarders
  (`cmd/rimsky-host-agent-proxy/{publisher,validation,data_processing}_handler.go`),
  each tunnelling through `forwardProxyUnary` to the agent's
  `forwardUnaryByMethod`. `unimplemented_handlers.go` is deleted. The embedded
  `genv1.Unimplemented*Server` structs that remain are the generated
  forward-compat base (for RPCs not yet in the proto), not no-op handlers.
- `ScopesConflict` is consulted in both the acquire path
  (`lib/runtime/runner_acquire_claims.go::evaluateClaimScopeConflict` →
  `scopesConflict`) and the fan-out sub-claim path
  (`lib/runtime/runner_subclaim.go::AcquireSubClaims`).
- The pg store has a real atomic-staging substrate (`staging.go`), `row_count_ratio`
  compiles in `sql-checks/compile.go`, and `pg/claim_unavailable` /
  `pg/swap_failed` have real emit sites reaching the operator's `error_types:`
  routing.
- The claude-agent signoff gate reconstructs the effective bound bag from the
  accumulated incremental `attributes_set` writebacks merged with the terminal
  delta (`agent-run.ts`), so the incremental path is no longer signed over `"null"`.
- The `breakpoint.hit` event-log row is appended co-transactionally with the hit
  (`lib/runtime/breakpoint_eval.go:277`).
- `run_scope_id` is on the executor wire and the proxy keys spawn isolation on it;
  per-binding `args`/`env`/`cwd`/`ready_timeout_seconds` are on the `Binding` proto
  and consumed in `lib/runtime/hostagent/spawn.go`.
- The ref-validation mode (`all`/`available`/`none`) is threaded config → validator,
  and the mandatory instantiation gate (`instances_static_config_gate.go`) runs
  full value-constraint validation at create-time.

(Several test comments in the diff say "a later pass adds X" / "FAILs today" —
these are the RED-phase expectation notes the plan's RED→GREEN structure
prescribes, describing the pre-fix state the test was authored against, not
deferred work on the final tree.)

---

## Divergences within the delivered work

The delivered stories build clean and their gates are real. The items below are
choices the plan/spec left unspecified, or load-bearing shapes that differ from
the spec's literal description while still meeting the user-observable outcome.

### 1. `watch` merges two still-separate sources client-side, not one unified `/events` stream

- **What the spec said (`S-cli-onboarding-watch-chronological`):** the end-state is
  "with breakpoint hits unified into the `/events` log (per
  `S-observability-breakpoint-hit-event`), `watch` drains the single
  timestamp-ordered `/events` stream (plus the terminal check)."
- **What was implemented:** `cmd/rimsky/cli/watch.go::RunWatch` keeps the
  two-source drain — the events route (`ListEvents`, lines 91-116) plus the
  separate breakpoint-hits route (`ListBreakpointHits`, lines 123-145) — and merges
  them client-side with `sort.SliceStable` on parsed timestamps (lines 150-152). The
  comment at `watch.go:71-74` makes the guarantee explicit: the sort is "strictly
  within-cycle (a hit that arrives in a later poll cannot reorder behind an event
  already printed in an earlier one)."
- **Inferred reason:** Even though `S-observability-breakpoint-hit-event` landed
  (breakpoint hits ARE now on `/events`), the watch verb was not re-pointed at the
  now-unified single stream; it still reads the dedicated breakpoint-hits route and
  merges. The story's user-observable outcome (a hit between two events by timestamp
  within a poll window) is met, but the mechanism differs from the spec's
  single-stream end-state and the cross-poll ordering guarantee is narrower than a
  globally timestamp-ordered stream.

### 2. Verifier severity consumes a services-local `checks.Severity`, not `spec.Severity`

- **What the spec said (`S-executors-verifier-severity-partition`):** "The typed
  `Severity` enum MUST be the value actually consumed by the verifier's failure
  classification, not an unused declaration." The story's `Today` named the unused
  `lib/foundation/spec/enums.go::Severity`.
- **What was implemented:** `lib/services/executors/verifier-shape-checks/checks/checks.go:51-69`
  defines a NEW `checks.Severity` (`"error"`/`"warning"`) with an
  `@source: lib/foundation/spec/enums.go::Severity` annotation, and the verifier
  consumes THAT. `spec.Severity` itself (and its `lib/graph/shared/types.go`
  re-export alias) remains unconsumed by the verifier.
- **Inferred reason:** the `consumption-side-isolation` depguard forbids
  `lib/services` from importing `lib/foundation`, so the foundation enum cannot be
  imported into the verifier; tracked-duplication-with-`@source:` is the project's
  sanctioned pattern. The user-observable partition (warning non-blocking, error
  blocking, severity actually consumed) is met. Recorded because the specific enum
  the story pointed at is still unconsumed and a new local type carries the behavior.

### 3. pgstore atomic-staging engages only for schema-shaped selectors; "swap collision" = non-empty/depended-upon canonical

- **What the spec said (`S-pgstore-atomic-staging-substrate`):** "use the postgres
  store as a real atomic-staging substrate: Open reserves a staging schema … Commit
  performs an atomic schema swap into the canonical view … `pg/swap_failed` if the
  swap fails."
- **What was implemented:** `lib/services/stores/postgres/store/staging.go::stagedScopeBytes`
  (and `commitStagingSwap`) gate the entire staging lifecycle on the selector matching
  `schemaIdentRegex` (`^[a-z_][a-z0-9_]*$`, `staging.go:97`). An opaque/path-shaped
  scope-bytes claim (e.g. `tenant/a/x`) is NOT a swap target — it keeps the verbatim
  selector-echo Open and the no-op terminals. The canonical drop is `DROP SCHEMA …
  RESTRICT` (no CASCADE, `staging.go:181-183`), so the swap "failure" that yields
  `pg/swap_failed` is specifically a populated / externally-depended-upon canonical,
  not an arbitrary swap fault.
- **Inferred reason:** gating on schema-shape lets one `staged_async` store keep
  serving opaque scope-bytes claims (the conformance suite and non-schema producers
  rely on the verbatim-echo path), and RESTRICT is a deliberate no-surprise-data-loss
  property. Recorded because the spec's "use the postgres store as an atomic-staging
  substrate" reads as universal, while the shipped substrate is scoped to
  schema-identifier selectors and defines "swap fails" as the populated-canonical case.

### 4. `pg/swap_failed` surfaces as a Commit-boundary classed error via gRPC ErrorInfo, not a tx-fatal Error frame

- **What the spec said (`S-pgstore-claim-unavailable-swap-failed-emit`):** the pg
  store emits `pg/swap_failed` when a swap fails, observable at a subscriber surface
  and routable through `error_types`.
- **What was implemented:** `commitStagingSwap` returns a
  `*store.ClassedError{Class:"pg/swap_failed"}`; `server/server.go::classedStatus`
  stamps the class into a `google.rpc.ErrorInfo` detail
  (Domain `rimsky.store-postgres`) on the gRPC Commit/Abandon/Release boundary, which
  the runtime peer decodes into the holder's `error_types:` routing. It is a classed
  error recovered at the terminal-verb boundary, not a tx-fatal executor `Error` frame.
- **Inferred reason:** letting a swap-failed error bubble out of Commit as tx-fatal
  would wedge the auto-terminal transaction; surfacing it as a routed classed signal
  is the load-bearing-correct shape. A seam the plan left open; the outcome
  (subscriber receives the class) is met.

### 5. `pg/claim_unavailable` required a new `error_class` field on the `Unavailable` proto message

- **What the plan said:** the only enumerated `[proto-edit]` passes were the
  HOSTAGENT ones (`run_scope_id`, host_agent verb routing, Binding overrides).
  AUTHSTORES surfacing of `pg/claim_unavailable` was framed without naming a wire change.
- **What was implemented:** `lib/protocols/proto/v1/claim_producer.proto` adds
  `string error_class = 1;` to `message Unavailable` (previously empty), threaded
  through `claimproducer.OpenOutcome.UnavailableClass`, the peer client, and
  `AcquiredLock.UnavailableClass` → `runner_lifecycle.go`, so `pg/claim_unavailable`
  reaches the operator's `error_types:` chain on the Unavailable arm.
- **Inferred reason:** `pg/claim_unavailable` fires on the Unavailable arm (not an
  executor Error frame), so giving the declared class a real subscriber-visible signal
  required carrying it on the Unavailable wire shape. Legal overshoot necessary to
  satisfy the story; recorded because a `.proto` field landed outside the plan's
  enumerated proto passes.

### 6. agent rate-limit Error class is policy-gated; the other three emit unconditionally

- **What the spec said (`S-executors-claude-agent-error-classes`):** the executor must
  emit all four declared classes as terminal `Error.error_class`, with the rate-limit
  case qualified "(with `cli.handle_rate_limits=false`)".
- **What was implemented:** `error-classify.ts` emits `agent/context_exceeded`,
  `agent/tool_use_failed/<tool>`, and `agent/refused` unconditionally; `agent-run.ts`
  emits `agent/rate_limited` as an Error class only when a rate-limit is detected AND
  `handle_rate_limits=false`. The default path keeps diverting rate-limits to the
  park (snooze) outcome.
- **Inferred reason:** within the spec's own `handle_rate_limits=false` qualifier —
  the default auto-park is preserved and the Error-class emission is reserved for the
  opt-out path. Recorded because three of four classes emit unconditionally while the
  fourth is policy-gated.

### 7. fs atomic-staging shipped as a fresh `examples/` producer, not a git-recovery of the deleted in-tree producer

- **What the spec/plan said (`S-fsstore-atomic-staging-reference`):** the story is
  framed around the stage-then-swap filesystem reference producer deleted in `c1ce756`,
  plus "a copyable pattern doc/example."
- **What was implemented:** a fresh, Apache-licensed `examples/atomic-staging-fs-producer/`
  module (cmd, server, store, sweep, README, `template.yaml`, behavioral + sweep tests)
  with a real POSIX `os.Rename` swap. (Separately, the bundled
  `lib/services/stores/filesystem/store/store.go` itself now carries staging /
  cross-filesystem-rename logic, so the bundled store is no longer purely sync-only.)
- **Inferred reason:** `examples/` is the durable home for a copyable reference under
  the workspace gate (Apache-2.0, dependency-isolated), matching the spec's "follow a
  copyable pattern doc/example" clause. The user-observable outcome (a real
  stage-then-swap producer with atomic rename on Commit / discard on Abandon, exercised
  by a gated test) is met. Recorded because the artifact is a new example rather than a
  git-recovery of the deleted producer.

### 8. `rules.md` stores-redesign example dropped rather than repointed

- **What the plan said (CLICTRL-1.2 step 4):** repoint the
  `(e.g. docs/2026-04-25-stores-redesign.md)` example to
  `(e.g. .ok-planner/sketches/2026-04-25-stores-redesign.md)`, with a fallback: "If a
  `.ok-planner/sketches/` file by that exact name is not on disk, drop the parenthetical
  example entirely."
- **What was implemented:** `.claude/rules/rules.md` reads "Design proposals go in
  `.ok-planner/sketches/` with a YYYY-MM-DD prefix." — the parenthetical example was
  dropped, not repointed.
- **Inferred reason:** sanctioned fallback — no
  `.ok-planner/sketches/2026-04-25-stores-redesign.md` exists on disk, so the plan's own
  contingency directs the drop. Recorded for completeness, not as a defect.

---

## Gate corrections (repaired by the executor)

None observed. The pass-level RED/GREEN gates that landed across all seven prefixes
were sampled and are real; no gate was found weakened to pass. The four workspace
modules and `examples/` all build clean, and the named full-stack acceptance gate
exists for each story (contradicting both prior versions of this file, which were
stale snapshots claiming those gates were absent).

---

## Orchestration record + walk resolution (appended after the divergence walk)

These are not code-vs-plan divergences the per-pass auditor sees; they record how the run
was orchestrated and what the post-run divergence walk resolved.

### M1 — Plan normalized from 67 bundled passes to 92 RED/GREEN engine passes
The plan was authored for a bundled-gate model (each `## Pass` authored its own test AND
fixed it). The flip-gating engine requires a pass's gate to be red-at-pre-flight, so each
bundled pass was split into a RED sub-pass (authors the failing test, gated `! <test>`) and
a GREEN sub-pass (implements, gated `<test>`) — the shape AUTHSTORES already used. Proto-edit,
design-change, recovery, and proof-gap passes ran as no-flip-gate passes (the implementer
self-verifies). Net: the 67 `## Pass` entries ran as 92 engine passes across six cluster
workflows; no task content changed, only the pass/gate structure.

### M2 — `## Acceptance` gates authored as a separate (7th) run
The plan parked its 43 end-to-end acceptance gates in a `## Acceptance` section with no owning
`## Pass`, so the engine (which runs `## Pass` entries) did not author them. 31 were the per-pass
GREEN tests already on the tree; the 11 full-stack e2e gates that were NOT (6 CLICTRL, 5
TEMPLCASCADE) were authored in a dedicated run and verified green against the real product.

### M3 — Fixed during finalization: 18 missing/wrong license headers
The 15 git-recovered `cmd/rimsky/cli/compose/*.go` files carried Apache headers but `cmd/` is
AGPL; `tools/rulesdoc/{doc.go,rulesdoc_test.go}` and `lib/services/executors/claude-agent/src/env-refs.ts`
had none. Corrected per `licensing.yml` (cmd/tools → AGPL, claude-agent → Apache); `license-check`
now reports 0 violations.

### M4 — Fixed during finalization: `TestParkedLifecycleResumeOnDeadline` load flake
A pre-existing scenario test (not plan work) flaked under full-suite load — an accumulated-latency
wall-clock race: a redundant `phase='parked'` probe ran after the park-signal/lineage probes, so
their latency could push it past the 10s resume deadline (the sweep woke the node first → observed
`completed`). Fixed by moving the deadline-sensitive probes to immediately after the parked
transition and bumping the budget to 15s; verified green in isolation (×3) and under `-parallel 8`.

### Walk resolution of Divergence 1 (watch) — REWORKED
Found during the walk to be a real double-print bug: `breakpoint.hit` now lands on `/events`, but
`RunWatch` ALSO drained the pending-breakpoint-hits route and rendered both, printing every hit
twice; the acceptance test only asserted ordering, not dedup. Reworked — `RunWatch` now drains
`/events` alone (the single chronological stream the spec's end-state named), renders `breakpoint.hit`
rows with their checkpoint/mode, the dead `printWatchHit` is removed, and the e2e test now asserts
no pending-hits-route row appears. The pending-hits route is unchanged — it is a live point-in-time
status surface (`instance status`, the MCP hits resource), not dead code. Unit + e2e + lint green.

### Walk resolution of Divergence 3 (pgstore staging scope) — ACCEPTED
The schema-shape gating and `RESTRICT`/`pg_swap_failed` collision semantics are the intended
design, documented in the `2026-06-06` Notes entry on `concept:atomic-staging` ("The lifecycle
engages only for schema-shaped selectors… a populated or externally-depended-upon canonical is
refused rather than silently clobbered, surfacing pg/swap_failed"). The implementation matches the
durable design; accepted, no rework.
