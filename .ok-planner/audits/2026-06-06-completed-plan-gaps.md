# Rimsky Gap Audit — Completed Plans Only

**Scope (narrowed):** the *only* question here is — **which completed plans/specs, or
shipped documented guarantees, claimed a deliverable as DONE that the code does not
deliver?** Forward-facing roadmap (anything whose only home was an un-executed sketch
under `.ok-planner/sketches/`, or an issue the project already tracks as an *open
tension*) is out of scope and listed, named, in the "Removed" section so you can see it
was deliberately excluded — not overlooked.

This replaces the broad `2026-06-05-intent-vs-reality-gaps.md` audit, which mixed
never-promised roadmap into the gap list. That file is left in place as the superset.

Every kept entry below carries **both** the completed-artifact claim **and** the
current-code evidence (path:line read 2026-06-06). All findings were re-verified against
current code, not the point-in-time divergence records — see "Method" at the end for why
that mattered.

---

## Executive summary

**Of the work that was supposedly finished, very little is actually broken.** Once the
forward-facing roadmap and the project's own open tensions are removed, the set of
"allegedly completed but really wasn't" collapses to a small, honest list — and it is
*not* shaped like "we planned a feature, marked it done, and it doesn't work." The core
orchestration engine the completed plans built is real and tested.

The reason no client has gotten Rimsky working end-to-end is **one blocker**, and it is
not in the engine — it is in the on-ramp:

> **The documented headline verb `rimsky run <file>` ships with no file to run.** The
> command and decoder are correct and complete, but there is no copyable example
> `TemplateSpec` anywhere in the tree, no fenced code in the README, and the only working
> spec shapes are buried in Go test structs. A new operator told to "run a file" has
> nothing to copy.

Stacked on that are **two shipped guarantees the code does not hold** — the
`Idempotency-Key` "MUST" (the doc overstates it; the completed plan and the code both
treat it as optional) and blessed-invariant 9b (documented as enforced + tested; it is
neither). These are the corrosive ones, because operators *rely* on documented MUSTs.

Everything else that survived the re-scoping is a **tail of low-blast deferrals**:
completed plans that shipped with a test at the wrong altitude, a plumbing task skipped,
or a doc-comment that overstates behavior. Each is a real intent-vs-reality gap (the plan
named it and it didn't land), but none breaks a running system.

So the honest answer to "how much of the finished work actually shipped working" is:
**almost all of it.** The end-to-end failure is concentrated in the first-run experience,
not the runtime.

| # | Gap | Category | Blast |
|---|---|---|---|
| 1 | `rimsky run <file>` ships no copyable example spec | completed-not-delivered (onboarding) | **blocker** |
| 2 | `Idempotency-Key` "MUST" is doc overstatement | unenforced-guarantee / doc-drift | medium |
| 3 | Blessed-invariant 9b has no enforcing site + no conformance probe | unenforced-guarantee | medium |
| 4 | sensor-cron state-DB plumbing skipped | deferred-never-resumed | low |
| 5 | wait-set `topic_kind` collapses the 5-value taxonomy to 3 | deferred-never-resumed | low |
| 6 | MCP-auth conformance probe (`--mode=auth-mcp`) never built | deferred-never-resumed | low |
| 7 | idempotency / capability-check unit-test matrix not added | deferred-never-resumed (test) | low |
| 8 | embedded-source lenient-`?` recovery e2e missing | deferred-never-resumed (test) | low |
| 9 | `await_async`-stuck + backfill-override only unit-tested | deferred-never-resumed (test) | low |
| 10 | Quality-rule typed `Severity` enum is unwired | completed-partial | low |
| 11 | Negative `max_signoff_attempts` parse-vs-gate mismatch | completed-buggy (unreachable) | low |
| 12 | `rules.md` still cites removed `deploy/` artifacts | doc-drift | low |
| 13 | `watch` "chronological feed" is source-grouped per poll | doc-drift | low |
| 14 | "Five source kinds" docstring over six bullets | doc-drift | trivial |

---

## A. The blocker — completed onboarding path with nothing to run

### 1. `rimsky run <file>` has no copyable example `TemplateSpec`

- **Completed artifact claimed it:** the `rimsky run` verb + CLI shipped via the completed
  CLI/compose plan (`.ok-planner/archive/2026-05-02-rimsky-cli-and-compose-plan.md`), and
  the in-repo `README.md` presents `rimsky run <file>` as the first dev-loop step
  ("register, deploy, instantiate in one shot" from a YAML `TemplateSpec`).
- **Code reality (verified 2026-06-06):**
  - Engine is correct and complete — `cmd/rimsky/cli/run.go` and `readSpecFile` in
    `cmd/rimsky/cli/templates.go`.
  - **No copyable spec ships.** `grep -rln 'nodes:'` over all non-scratch/non-generated
    YAML returns **zero** hits. `README.md` has **zero** fenced code blocks. `examples/`
    holds four gRPC protocol-service Go programs (`claimproducer/`, `executor/`,
    `lifecyclesubscriber/`, `publisher/`) — not deployable specs. The only `TemplateSpec`
    shapes in the tree are inline Go structs in test files (`test/scenarios/canary/…`,
    `cmd/rimsky/cli/templates_test.go`).
- **Blast radius:** this is the single most likely reason a fresh consumer never reached a
  documented end-to-end flow. The user is told to run a file; there is no file to copy, so
  they must reverse-engineer the YAML shape from Go test source before anything documented
  is reachable. The engine being fine is exactly what makes this so costly — the work that
  was *finished* is unreachable for want of one example artifact the plan never produced.
- **Fix shape:** add one illustrative, generic-named `TemplateSpec` YAML under `examples/`
  exercised by a test, and a fenced quickstart in `README.md`. Low scope, high leverage —
  it converts the documented happy path from "reverse-engineer it" to "copy and run."

---

## B. Unenforced documented guarantees (most corrosive — operators rely on them)

### 2. The `Idempotency-Key` "MUST" is a documentation overstatement

- **Shipped guarantee:** `CLAUDE.md` cross-cutting gotcha — *"Every publisher message-emit
  **MUST** carry the `Idempotency-Key` HTTP header."*
- **What the completed plan actually intended:** the sensor-messaging-unification plan
  (`.ok-planner/history/plans/2026-05-17-sensor-messaging-unification.md`, Task 12) named
  a test `TestCreateMessage_NoIdempotencyKeyCreatesNewMessageEachTime` — i.e. the plan
  intended a *missing* header to be **allowed** (create a new message each time, dedup only
  when a key is present). The code matches the plan, not the doc.
- **Code reality (verified 2026-06-06):** `lib/control/controlapi/messages.go:167` reads
  the header; `:217` gates dedup behind `if idempotencyKey != ""`; `:301` returns
  `201 Created`. There is **no** 400 guard for a missing header. The checked-in test
  `lib/control/controlapi/messages_test.go` POSTs with no key and asserts 201.
- **Blast radius:** the documented MUST is false. A publisher that omits the header — which
  the doc says is impossible — silently loses replay-dedup, so provider retries can
  double-fire the cascade with no diagnostic. The danger is precisely that an operator
  *trusts the MUST* and assumes dedup-by-default.
- **Fix shape (a product call, surfaced not taken):** either (a) soften `CLAUDE.md` to
  "optional dedup key — dedups when present" (matches the completed plan's intent and the
  code), or (b) make the doc true by adding a 400 guard for a missing header and flipping
  `messages_test.go`. The completed plan's own test names point at (a).

### 3. Blessed-invariant 9b has no enforcing code site and no conformance probe

- **Shipped guarantee:** `CLAUDE.md` "Load-bearing safety properties" — every
  `@blessed-invariant` *"carries the invariant plus the code site that enforces it;
  scenario tests under `test/scenarios/` exercise them."*
- **Code reality (verified 2026-06-06):** invariant 9b ("ClaimProducer implementations MUST
  NOT internally serialize on lock-shaped predicates") exists only as interface comments at
  `lib/protocols/claimproducer/claimproducer.go:23` and `lib/foundation/locks/interface.go:54`.
  There is **no enforcing code site** — it constrains *third-party producer internals*, which
  rimsky cannot structurally enforce — and **no conformance probe** in
  `lib/protocols/conformance/claimproducer/runner.go`. The scenario test exercises only the
  rimsky-side consequence via rimsky's own predicate, not a producer's compliance.
- **Blast radius:** a consumer shipping a `ClaimProducer` with the forbidden reader-lease
  serialization silently violates 9b. Reads that should run concurrently serialize, with no
  diagnostic at handshake or in conformance — the exact failure the "enforced + tested"
  guarantee promised to catch. 9b is the one blessed invariant the documented guarantee
  cannot keep; the doc should say so (mark 9b "advisory — not machine-checkable") or a
  conformance probe should be added that exercises a producer's concurrency behavior.

---

## C. Recorded as deferred in a completed plan, never resumed

These are honest punts: the completed plan named the item, shipped knowing it was a hole,
and the follow-up never happened. Each is a real intent-vs-reality gap; all are low-blast.

| Gap | Completed plan that named/deferred it | Code reality (2026-06-06) | Why it's low |
|---|---|---|---|
| **sensor-cron state-DB plumbing** | `history/plans/2026-05-17-sensor-messaging-unification.md` Task 50 + spec Stage 3: "state-DB env-var is plumbed, in-memory default." Divergence §1 records it skipped. | No `sensors/sensor-cron/state_db.go`; no `RIMSKY_SENSOR_CRON_STATE_DSN` plumbing. | `next_fire_at` reconstructible; runtime resync restores subs (≤1 missed fire per restart). |
| **wait-set `topic_kind` enum** | `history/plans/2026-05-23-signal-taxonomy-and-policy-decoupling.md` divergence deferred the CHECK-broadening migration. | Runtime adapter `waitSetTopicKindFor` collapses the 5-value signal taxonomy into the legacy 3-value enum. | Lossy only for the wait-set discriminator; the audit log retains the full type-path. |
| **MCP-auth conformance probe** | `history/plans/2026-05-15-control-plane-mcp-and-auth.md` divergence deferred `--mode=auth-mcp`. | No conformance-binary auth-mcp mode; the behavioral (M2) assertions exist as in-process MCP unit tests + a by-grant scenario test. | Behavior *is* covered; only the runnable packaging is missing. |
| **idempotency / capability unit-test matrix** | `history/plans/2026-05-17-sensor-messaging-unification.md` Tasks 12 & 32 named 8 test functions (status-code matrix); divergence §3 records only 3 pre-existing tests remain. | Behavior covered e2e by `runtime/sweep_message_idempotencies_test.go` + `test/scenarios/sensor/message_routing_test.go`; the per-status unit matrix is absent. | Coverage altitude only; the paths are exercised end-to-end. |
| **embedded-source lenient-`?` e2e** | `history/plans/2026-05-20-multi-source-substitution-decline.md` divergence. | Embedded-source + `\| <literal>` fallback ARE covered e2e (`embedded_source_test.go`, `fallback_test.go`); only the `z_pattern_producer_recovery` lenient-`?`-marker e2e is missing (the `?` marker is unit-tested). | One e2e variant of an otherwise-covered feature. |
| **`await_async`-stuck terminate + backfill-override scenario** | `history/plans/2026-06-03-instance-lifecycle-durable-by-default.md` / `…/2026-05-28-quality-of-life-features.md` — the spec strategy demanded full-stack scenario coverage. | Both behaviors tested at handler/runtime-unit altitude with fakes; no full-stack scenario as the completed spec demanded. | No behavioral risk; the altitude is below what the spec asked for. |

---

## D. Completed-partial / completed-buggy

| Gap | Completed plan | Code reality (2026-06-06) | Severity |
|---|---|---|---|
| **Quality-rule typed `Severity` enum is unwired** | `history/plans/2026-05-28-quality-of-life-features.md` introduced a typed `Severity` enum (retiring the `== "warning"` string footgun). | The typed enum exists with **zero consumers**; verifier-shape-checks treats every failure as blocking — the warning/error partition the enum was meant to express was dropped. | low — completed-partial |
| **Negative `max_signoff_attempts`** | `history/plans/2026-06-04-claude-agent-signoff-gate.md`. | Parser keeps negative values; the gate coerces them to 3. Unreachable in practice — the JSON-schema `minimum: 0` rejects negatives at registration before the gate runs. | low — completed-buggy (cosmetic / unreachable) |

---

## E. Doc-drift shipped inside completed work

Stale instructions/comments a completed plan left behind. Cheap to fix; high-confusion
because a reader (or a future session) follows them.

| Gap | Completed plan that caused the drift | Code reality (2026-06-06) |
|---|---|---|
| **`rules.md` cites removed `deploy/`** | A completed reorg plan (`history/plans/2026-05-24-repo-reorganization.md` / `…/2026-05-27-root-folder-reorg.md`) removed `deploy/`. | `.claude/rules/rules.md:20` still instructs rebuilding via `deploy/build-images.sh` and bringing up `deploy/docker-compose.yml`; **no `deploy/` dir exists**. Real path: `make core-images` + the testcontainers harness. (`/health` route itself exists.) This one bites every session that follows the project rule. |
| **`watch` "chronological feed"** | `history/plans/2026-05-24-instance-debugger.md`. | `cmd/rimsky/cli/watch.go:62-142` is source-grouped per poll cycle (events → hits → terminal), not timestamp-interleaved; the doc-comment at `:8-9` overstates it as chronological. Every row still carries its true timestamp. |
| **"Five source kinds" docstring** | substitution shipped via the completed attribute-pull / multi-source plans. | `lib/graph/attribute/substitution.go:7` header says "Five" over six bullets and omits `trigger`/`child`; the resolver itself is correct (`doc.go:35` is right). Pure docstring drift. |

---

## F. Removed from this revision — forward-facing roadmap (never promised by a completed plan)

These appeared in the broad audit but are **out of scope** here: each one's only home was an
un-executed sketch, or it is an issue the project already tracks as an **open tension**
(i.e. acknowledged-unresolved — the opposite of "allegedly completed"). They are listed so
the exclusion is visible, not silent.

**Only-ever-a-sketch (never executed):**
- Package manager CLI + catalog
- `geo` blessed typed-attribute (PostGIS/GeoParquet/CRS)
- Rimsky Development Kit (Python `rimsky-rdk` + `python-runtime` executor)
- Distributed tracing (OpenTelemetry spans / `traceparent` / trace columns)
- SSE event stream `GET /events/stream` (+ LISTEN/NOTIFY)
- Template-level message schema (`messages:` block)
- Durable audit-log spool + background shipper
- Dynamic executor / claim-producer registration endpoints (`POST`)
- Breakpoint-hit push delivery + `breakpoint.hit` event kind
- Unified child-execution primitive (`DispatchChildExecution`/`SettleChildExecution`) + `carry_verbatim`
- Executor subprocess lifecycle events (`executor.subprocess_*`, `cost_recorded`)
- Heartbeat enrichment columns (`last_subprocess_stdout_at`, `last_callback_kind`)
- One-message-per-frame invariant
- Multi-tenant substore provisioning verbs
- `report_await_async_callback` MCP tool
- `_rimsky.run_scope.*` reserved attributes to fan-out children
- Reactive-nomenclature rework (event/emit/subscribe rename)
- Cheap-test stub compose profile + synthetic-blocker binary
- Single shared delegation/fan-out runtime primitive (internal refactor)
- **Bundled-store `SplitScope`** (postgres + filesystem + its conformance coverage) — this
  is the one the broad audit ranked **High**, so it deserves a sentence: `concept:fan-out`
  puts split-scope on **the producer** ("the claim's producer MUST advertise split-scope
  support… otherwise template registration rejects") and nowhere claims the *bundled* stores
  implement it. The bundled stores embed `UnimplementedClaimProducerServer` and a `fan_out:`
  node against them is rejected at registration — which is **exactly the shipped concept's
  documented behavior**. Bundled-store split-scope was only the 2026-05-28 sketch. So it is
  not a broken completion; it is unbuilt roadmap. (Worth building, but out of scope here.)

**Tracked as an open tension (acknowledged-unresolved, not a false completion):**
- Anonymous-mode + late-bound services mutually exclusive
- SQLite + replicas>1 has no symmetric fail-fast startup gate
- Callback `advertise_host` fails silently for routable typos
- `compose:` prefix reservation enforced client-side only (server has no guard)
- Coalesced-fire produces no insert-vs-coalesce audit signal
- Stub-mode conformance signature has no single source of truth
- Unified 5×-heartbeat orphan cutoff is two base intervals, not one (invariant 6)

---

## G. Deliberately deferred & documented (not gaps)

Explicit, documented carve-outs where the code matches a completed decision.

- **`rimsky_events.kind` is free-form (no enum).** `concept:event-log` documents this as
  intentional — *"The `kind` column is free-form; no enum constraint. Zero-migration to add
  a new kind; the price is that typos produce events no consumer finds"* — and points at the
  open `tension:events-kind-no-enum`. The node-run-transition subset already carries
  canonical signal type-paths. Working as designed.
- **Dashboard reference SPA not in repo.** Deliberately carved to a sibling repo (commit
  c1ce756); `feature-index.md` affirmatively documents the absence. Backend observability
  surfaces remain in-repo. The dashboard spec is the only active (un-executed) spec.

---

## H. Themes

1. **Engine real, on-ramp missing.** The defining pattern. The completed plans built a
   runtime that is implemented and tested, but the first-use affordance was never produced:
   `rimsky run <file>` with no example file is the whole ballgame for "no client got it
   working." Treat the documented happy-path as a tested artifact (one example spec, one
   onboarding scenario) and the headline failure goes away.

2. **Docs claim more than the code holds — at the guarantee layer.** The `Idempotency-Key`
   MUST and invariant 9b's "enforced + tested" are the corrosive class: not missing
   features, but *promises* operators build on. The mitigation is mechanical — every
   `CLAUDE.md` "MUST" and every `@blessed-invariant` should have a test that fails when the
   guarantee is removed; where a guarantee is structurally unenforceable (9b), the doc must
   say "advisory," not "enforced."

3. **Completed plans punt tests to a lower altitude and call it done.** The deferred tail is
   dominated by "tested with fakes at unit level, full-stack scenario deferred" and "test
   matrix named but not written." The behavior is usually covered; the *altitude* the spec
   asked for is not. This is benign per-item but is the systemic reason "completed" doesn't
   equal "acceptance-proven."

4. **Reorganizations leave doc-drift in live rules.** The `deploy/` references in
   `.claude/rules/rules.md`, the `watch` "chronological" comment, the "Five source kinds"
   header — small stale pointers shipped by completed plans that send the next reader (or
   session) chasing artifacts that moved or never existed. Sweep them when touching the
   adjacent code.

**Bottom line:** the suspicion that "plans were completed but the features don't work" is
**mostly not borne out** for the orchestration engine — the finished work is largely real.
The end-to-end failure is one missing onboarding artifact, plus two over-stated guarantees.
Fix the example spec, reconcile the two guarantees with reality, and the documented core
workflow becomes reachable and trustworthy.

---

## Method — why every claim was re-checked against current code

The completed plans' `-divergences.md` records are point-in-time. Re-verifying against
current code (2026-06-06) caught **two divergence-recorded gaps that have since been
closed**, which would have been false findings if carried forward:

- **`rimsky_messages.sender_kind` CHECK** — the 2026-05-17 divergence recorded the CHECK
  still allowed `'sensor'` not `'publisher'` (a `'publisher'` insert would have failed).
  Current `lib/foundation/persistence/{postgres,sqlite}/migrations/001-schema.sql` reads
  `CHECK (sender_kind IN ('operator','publisher','instance'))` — fixed in a later
  migration-baseline flatten. Not a live gap.
- **Callback-determinism TOCTOU** — the 2026-05-22 fan-out divergence recorded
  `applyTerminal` running outside the phase-check tx as a deferred TOCTOU window. Current
  `lib/runtime/callback.go:516-522` documents the window as **closed** ("the TOCTOU window
  between commits closed when applyTerminal was rewritten to accept the outer tx"). Not a
  live gap.

That the engine is actively closing its own recorded holes is itself evidence for the
executive summary's conclusion: the finished runtime work is sound; the gaps that remain
are the on-ramp and the doc/guarantee layer.
