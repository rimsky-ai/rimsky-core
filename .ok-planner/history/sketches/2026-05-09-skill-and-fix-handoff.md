# Handoff: skill improvements + cycle-2 fixer audit

**Date:** 2026-05-09
**Origin:** end of multi-day platform-extensions delivery + design-log skill work
**Successor session:** pick this up to (1) audit and address the cycle-2 fixer's work, (2) update the remaining skills, (3) try it all in a clean run.

This file lives at `.ok-planner/sketches/` because it's not a feature sketch in the `/sketch` sense, but the `sketches/` directory is the closest fit for cross-cutting working notes. Treat as workflow scratch — delete after the work it describes is done.

## Where things stand

**Repo state (rimsky):**
- HEAD: `6ea77ef feat: platform extensions for agent-driven consumers` — the safety-net commit covering the eight implementation dispatches + four review-cleanup cycles.
- Working tree: dirty with the cycle-2 holistic-review fixer's changes (post-`6ea77ef`). NOT committed. The fixer report listed:
  - Replaced unwired `DiagnosticReader` interface with concrete `Queue.ListParkedDiagnostic` accessor + postgres + sqlite impls
  - Wired `StartGaugeRefresher` in supervisor / scheduler / control-api binaries
  - **Deleted** ~1k LOC of unused MCP catalog code (`executors/claude-agent/src/mcp-catalog.ts`, `mcp-resolver.ts`, `mcp-transports.ts` + their tests)
  - **Deleted** `mcp-servers/control-api/main.go` placeholder
  - Added `on_event_handler_fired` audit-log emission with `resolve` + `error_class`
  - Threaded `UserdataValidator` and `Metrics` into `CallbackServer`
  - Threaded `OrphanBlobSweepInterval` through scheduler config
  - Started discovery `RefreshLoop` in supervisor (was only running in control-api)
  - Fixed `probeStubMode` to return errors properly (`--require-stub-mode` was weaker than docs)
  - Now warns on blob-backend-mismatch silent payload drops
  - Renamed `InvalidateHandler` struct → `InvalidateAdapter` (removes naming collision with `RunArgs.InvalidateHandler` function field)
  - Removed unused `supervisorID` parameter from `ResumeParkedInTx`
  - Implemented userdata-schema cache (SHA-256-keyed) in `userdata_validator.go`
  - Renamed shadowed `cap` builtin in `runner_terminal_errors.go` and `retry_loop_cap_test.go` to `maxRetries`
  - Made `shouldSpillBlob` a one-line wrapper over `persistence.ShouldSpillBlob` with `@source` annotation
  - Removed several stale comments + the `var _ = errors.Is` import-buster
  - Made `makeStoreHandle` refuse non-JSON address bytes with an error (per blessed-invariant 20)
  - Updated CHANGELOG with per-fix bullet list
- The fixer reported `make build-all`, `make lint`, `make test-all`, and `npm test` (92 tests) all clean. Unverified after their report; verify before proceeding.

**Repo state (marketplace):**
- `../fgc-claude-marketplace/plugins/ok-planner/skills/init-design/SKILL.md` — new, complete
- `../fgc-claude-marketplace/plugins/ok-planner/skills/merge/SKILL.md` — new, complete
- `../fgc-claude-marketplace/plugins/ok-planner/skills/review-holistic/SKILL.md` — written earlier in this session, complete
- `../fgc-claude-marketplace/plugins/ok-planner/skills/init/SKILL.md` — updated to provision `.ok-planner/design/` and to carve out the design log as the consult-this exception
- `../fgc-claude-marketplace/plugins/ok-planner/skills/ok-planner/SKILL.md` — registers init-design, merge, review-holistic in the skills table

The marketplace plugin changes are uncommitted. They were edited in place via Edit/Write tools. No git commits attempted in that repo.

## Background context for the next session

This was a long delivery (~22.7k LOC across 180 files in commit 6ea77ef plus the fixer's working-tree delta). Key observations from the conversation:

1. **Reviews steer attention.** Generic `review-work` prompts find what the reviewer naturally pattern-matches to. We discovered that an explicit four-class review prompt (real issues / extraneous accretion / inconsistencies / churn) — codified as `review-holistic` — surfaces classes of issue (parsimony, dead-by-implication code, fat interfaces) that `review-work` consistently missed.

2. **Convergence is "no more extraneous accretion," not "issue count drops to zero."** Adding code in response to a real gap is correct; the codebase getting more complete is convergence. Divergence is when the codebase grows things that don't earn their keep.

3. **The reviewer doesn't have conversation context.** I (the orchestrator) had it; that's how I caught the cycle-2 reviewer's false positives. A fresh reviewer reads code statically. The structural fix is a durable design log (the new `init-design` + `merge` skills) so reviewers can distinguish design choices from defects.

4. **The cycle-2 fixer might have made mistakes** — see audit work below.

## Item 1: Audit and address the cycle-2 fixer's work

### Step 1 — verify the fixer's claims

Run from the rimsky repo root:

```
cd /Users/patrick/Documents/projects/research/zonebase/submodules/rimsky
git status --short
make build-all
make lint
make test-all
cd executors/claude-agent && npm test && npm run build
```

If any fail, debug and fix. The fixer reported all green; verify.

### Step 2 — diff the fixer's work for accuracy

```
cd /Users/patrick/Documents/projects/research/zonebase/submodules/rimsky
git diff --stat
git diff
```

Walk through the diff with a critical eye. Look for:
- **Architectural over-reach.** The fixer reportedly added a concrete `Queue.ListParkedDiagnostic` method on persistence and removed the `DiagnosticReader` interface. Verify: did this introduce a layering violation (modeling/controlapi reaching into foundation/persistence by going around the interface)? Or is it a clean simplification?
- **Deletions that were premature.** The MCP catalog was deleted with the rationale "imported only by tests." Verify: was that actually true? `grep -rn "loadMcpCatalogConfig\|resolveMcpServers\|materializeBindings" executors/claude-agent/src/` excluding `*.test.ts`. If non-test call sites exist, the deletion was wrong.
- **Whether the fixer addressed each finding correctly.** The cycle-2 review (in this conversation history, dispatched right before the fix call) had ~35 findings across 4 classes. The fixer's report lists ~30 fixes. Cross-check: which findings were skipped or partially addressed?

### Step 3 — address the cycle-2 reviewer's NEW findings (post-fix re-review)

The cycle-2 fixer's verification re-review (also in this session) found ~33 new findings. Some were real new bugs the fix introduced or surfaced; some were the reviewer not understanding the working tree. Specifically:

**False positives from working-tree blindness** (skip these — they were already addressed by the fixer's deletions):
- "MCP catalog files still committed" — they're committed in HEAD but deleted in working tree; commit will close.
- "mcp-servers/control-api/main.go placeholder still in HEAD" — same.

**Genuinely real new bugs (likely real, fix them):**
- **`{{rimsky.resume_payload}}` and `{{rimsky.resume_reason}}` are documented but not implemented.** `executors/claude-agent/src/agent-run.ts` builds `promptVars.rimsky = rimskyVars` but `renderTemplate`'s regex is `\{\{(userdata|attributes)\.([^}]+)\}\}` — only userdata/attributes namespaces are matched. The J10 resume-context template variables don't reach the prompt.
- **`schedule_fired` vs `schedule_fire` mismatch.** `modeling/scheduler/schedule_ticker.go` emits `Reason: "schedule_fired"` (past tense); `foundation/integration/cascade_invalidate.go::invalidateSourceBucket` switches on `"schedule_fire"` (present tense). Cron-fired invalidates always land in `"other"`; `rimsky_invalidates_total{source_kind="scheduler"}` for cron is permanently zero.
- **claude-agent observability ledger mislabels park/blocked outcomes as `step_completed`.** In `executors/claude-agent/src/server.ts` around line 401-405, the outcome-to-category map only handles complete/errored explicitly; everything else (blocked, park_requested) falls into `step_completed`. Honest labeling would have separate categories.
- **`InvalidateNode` "running | failed → 409" doc claim is wrong.** `modeling/controlapi/admin_diagnostics.go:203` claims both states return 409; `foundation/integration/wake_parked.go:92-97` only rejects `running`. `failed` falls into the default branch (frame-engine invalidate). Doc-vs-code mismatch.
- **`makeStoreHandle` ctx discard line is wrong.** `foundation/integration/runner_acquire.go:682` says `_ = ctx // ctx no longer used post-envelope refactor` — but ctx IS still used four lines above. Dead discard.
- **48 stale `core/...` doc references** across `foundation/`, `modeling/`, `cmd/`, `stores/`, etc. Pre-existing churn from the layer-crystallization rename. Pre-v1, fix all of them. `grep -rn "core/" --include="*.go" .` and update each to the post-Phase-5 path.
- **`LockHolder` / `LockHoldersStore` naming stale post-Phase-5.** Schema is `rimsky_claim_handle`; the interface is still named `LockHoldersStore`. Rename to `ClaimHandlesStore` (or similar) — pre-v1 break-freely.
- **`acquisition.DispatchID` / `RunnerResult.DispatchID` field names stale.** Schema is `rimsky_worker_request`. Rename to `WorkerRequestID`.
- **`MetricsHook` is a 14-method fat interface** mixing dispatch instrumentation and gauge refresh. The gauge-refresh methods (`SetNodesByState`, `SetParkedByReason`, `SetHeldFrames`, `SetDispatchQueueDepth`) belong in a separate `MetricsRefresher` shape. Split it.
- **SQLite migration's `PRAGMA schema_version = 1000000`** is a literal magic number; Postgres mirror uses idempotent `ADD COLUMN IF NOT EXISTS`. Document the dialect asymmetry at the top of the SQLite migration with a clear comment about why and when this is safe (pre-v1; the schema version is checked but not stored — verify).
- **Stale "deferred to v2" comment in `executors/claude-agent/src/observability.ts:18-19`** — gRPC observability service IS registered now. Update.
- **Stale file-header comment in `foundation/persistence/postgres/queue_park.go:11-15`** — claims `parked→active under fresh claimant id`; actual is `parked→pending` and supervisorID is dropped. Update.

**Plausibly-design (do NOT auto-fix; surface to user):**
- "templateVars.userdata duplicates the top-level userdata bag" — by-design DSL namespace.
- "ReasonHandlerError audit-log marker only" — debatable; the constant exists so state-machine tests confirm rejection. Discuss before changing.
- "userdata-schema.ts is 94 lines, could be inlined" — debatable separation of concerns. Discuss.
- `Store = ClaimProducer` deprecated alias — has callers; either delete (and rename callers) or drop the deprecated note. Discuss.
- `ReasonWorkCompleted` deprecated alias — pre-v1 break-freely says drop it. Likely safe but confirm.
- `RunArgs.InvalidateHandler` function-field name — could be renamed to `InvalidateFn` for symmetry with the `InvalidateAdapter` struct rename. Discuss.

### Step 4 — commit

After verifying the fixer's diff and addressing the genuinely new bugs above, commit. The user wants visibility before commit; this isn't auto-mode reflexive commit. Suggested message style matches `6ea77ef`:

```
fix: address holistic-review findings (cycle 1)

[bullet list]

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
```

## Item 2: Skill updates

Three updates to `review-holistic`, plus parallel updates to `review-work` / `review-files`, plus an amendment to `brainstorm`.

### Update A: review-holistic — three changes

**File:** `../fgc-claude-marketplace/plugins/ok-planner/skills/review-holistic/SKILL.md`

#### A1. Working-tree awareness in the reviewer prompt

In the "How to find things" section, add an explicit instruction near the top:

> **Working-tree first.** Before forming findings, run `git status` and `git diff` to confirm what's modified relative to HEAD. The working tree is your subject — code that's been deleted in the working tree is not a finding even if it appears in HEAD. Likewise, code added in the working tree is in scope even though it's uncommitted.

#### A2. Required notes-file reading (not "informational")

Replace the existing "Implementation notes (informational, not a fence)" section with:

> ### Implementation notes (required reading)
>
> Read every `*-notes.md` under `.ok-planner/plans/` in full before forming findings. Read every file under `.ok-planner/design/` (the design log) in full. These document deliberate design choices. For each finding that overlaps a documented decision, you must explicitly explain why the documented rationale is wrong — not just flag the deviation as suspicious. Surface findings that contradict documented decisions only when the documentation itself is flawed (and say which line is flawed and why).
>
> A finding that disagrees with a documented decision without addressing the rationale is noise.

#### A3. New "Questions" output class — fifth class for plausibly-design findings

In the "Output Format" section, add a fifth class:

> ## 5. Questions (plausibly intentional)
>
> Findings where you suspect something is wrong but it could plausibly be a design choice you don't have context for. List them here so they reach the user but don't get auto-fixed.
>
> Use this class for:
> - "Could be inlined" / "could be flattened" / "could be unified" judgment calls
> - Helpers/abstractions whose justification depends on intent rather than provable wrongness
> - Naming-convention concerns where the convention itself is debatable
> - Things that look stale but might document a deliberate negative choice
>
> Do NOT use this class to dump uncertain findings. The bar is: "if I had to defend this in front of someone who designed it, what would they likely say?" If a plausible defense exists, it goes here. If no plausible defense exists, it's a real finding.

In the "What NOT to do" section, add:

> - Don't put findings in class 5 to soften them. Class 5 is for plausibly-design choices, not for findings you're unsure about. If you're unsure whether something is wrong, do more reading until you're sure, then put it in the right class.

In the "After Review" section, update to:

> If the reviewer found findings in classes 1-4, invoke `ok-planner:review-cleanup` with **only** classes 1-4 (the issues). Do NOT process, filter, summarize, triage, or rebut classes 1-4 yourself.
>
> Surface class 5 (Questions) to the user verbatim — do NOT pass them to review-cleanup. The user will discuss and decide.

### Update B: review-work and review-files — adopt working-tree awareness

Both prompts already say "uncommitted: git diff, git diff --staged, git status." The phrasing is OK but worth tightening with the same "working-tree first" framing as A1.

**File:** `../fgc-claude-marketplace/plugins/ok-planner/skills/review-work/SKILL.md`
**File:** `../fgc-claude-marketplace/plugins/ok-planner/skills/review-files/SKILL.md`

In each, add to the "How to find changes" / equivalent section:

> Run `git status` and `git diff` first. Working-tree state is your subject — code modified or deleted in the working tree is in scope even if HEAD looks different. Don't form findings about code that exists in HEAD but has been deleted in the working tree.

### Update C: brainstorm — write to the design log when a spec produces a decision

**File:** `../fgc-claude-marketplace/plugins/ok-planner/skills/brainstorm/SKILL.md`

This is the bigger amendment. Read the current skill in full first; the change is to add a step after the spec is approved:

> ## After the spec is approved
>
> If the spec contains architectural choices that should be recorded in the design log, write them to `.ok-planner/design/` as ADR-style files (one per decision; format per `init-design` skill).
>
> A decision belongs in the design log if it meets ANY of these:
> - It's a choice between alternatives where the rejected alternative is identifiable.
> - It establishes or changes a project-wide convention.
> - It declares a tradeoff explicitly (e.g., "we accept worse X to get better Y").
> - It's a negative choice (we deliberately do NOT do Z).
> - It supersedes a prior decision in `.ok-planner/design/` — in which case write a new file with `supersedes: <old-id>` per the format-durability rules.
>
> A decision does NOT belong in the design log if it's just a local implementation detail or a style choice. The bar is: would a reviewer benefit from knowing this when forming findings?
>
> Walk the candidate list with the user before writing. Each candidate gets confirm-as-is / edit / reject — same pattern as `init-design`'s walkthrough.

### Update D: review-cleanup — pass through class-5 questions, not filter them

**File:** `../fgc-claude-marketplace/plugins/ok-planner/skills/review-cleanup/skill.md` (note: lowercase `skill.md`)

The fixer prompt currently says "fix all of them." If the orchestrator only passes classes 1-4 to review-cleanup (per Update A3), this stays correct. No change needed in review-cleanup itself, but add a guard at the top of the file:

> ## What this skill expects as input
>
> The orchestrator passes review findings that should be auto-fixed. If a review skill produces a "Questions" or "judgment-call" class, those findings must NOT be in the input to this skill — they should be relayed to the user separately. This skill's fixer treats every input as something to fix mechanically; it has no triage.

## Item 3: Try it all in a new session

After items 1 and 2 are done, start a fresh session in the rimsky repo. Test path:

1. `/init-design` — bootstrap the design log against the current rimsky codebase. Walk the drafts with the user. This will surface what the new skill catches and where it falls short.

2. `/review-holistic` — run the holistic review. Verify:
   - Working-tree awareness: it doesn't flag committed-then-deleted files
   - Notes-file reading: it doesn't flag `parked → stale` (a documented deviation)
   - Class 5: plausibly-design findings end up there, not in classes 1-4

3. If review-holistic finds genuine issues, run review-cleanup; if it produces class 5 questions, surface them to the user as designed.

4. After things are cleaned up, commit the design log + any code fixes.

5. Optionally test `/merge` — make a small branch, merge it, run `/merge` against main.

## Reference paths

- rimsky repo: `/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/`
- ok-planner marketplace plugin: `/Users/patrick/Documents/projects/research/fgc-claude-marketplace/plugins/ok-planner/`
- platform-extensions plan: `.ok-planner/plans/2026-05-08-platform-extensions-for-agent-consumers.md`
- platform-extensions notes: `.ok-planner/plans/2026-05-08-platform-extensions-for-agent-consumers-notes.md`
- platform-extensions spec: `.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md`
- this handoff: `.ok-planner/sketches/2026-05-09-skill-and-fix-handoff.md`

## Open philosophical thread (for the user, not for action)

The user's framing of convergence — "no more extraneous accretion" rather than "issue count drops to zero" — is correct and sharper than what the existing skills encode. The skill set as-is encodes "fix every finding" but doesn't have a vocabulary for "is this finding actually wrong, or is it just looking at the code without context." The new design log + the Questions class in review-holistic are the structural fix; the durable convergence test would be: "does a fresh review-holistic against this codebase produce findings I disagree with?" If yes, the design log isn't covering enough ground. If no, we've converged on a meaningful sense.
