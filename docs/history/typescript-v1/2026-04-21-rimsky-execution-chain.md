# Rimsky v1 — Unattended Execution Chain

Entry point for the unattended implementation run across Plans A → B → C. Read this first before invoking any subagent-dev run.

## Artifacts

- **Spec**: `docs/specs/2026-04-21-rimsky-v1-design.md`
- **Design (background)**: `docs/cell-graph-design.md`
- **Plan A**: `docs/plans/2026-04-21-rimsky-plan-a-foundation-scheduler.md` — foundation + scheduler
- **Plan B**: `docs/plans/2026-04-21-rimsky-plan-b-deterministic-supervisor.md` — deterministic supervisor + end-to-end execution
- **Plan C**: `docs/plans/2026-04-21-rimsky-plan-c-agentic-control-api.md` — agentic execution + HTTP control API + reference binaries
- **Execution log** (live, updated during runs): `docs/plans/2026-04-21-rimsky-execution-log.md`

Plans execute in strict order: A, then B (which amends A in its Phase 0), then C (which amends A and B in its Phase 0). Each plan completes fully before the next begins.

## How to kick off

### Option 1 — manual kickoff (you type the command)

```
/subagent-dev docs/plans/2026-04-21-rimsky-plan-a-foundation-scheduler.md
```

When Plan A completes successfully (all tests green), invoke the next:

```
/subagent-dev docs/plans/2026-04-21-rimsky-plan-b-deterministic-supervisor.md
```

When Plan B completes:

```
/subagent-dev docs/plans/2026-04-21-rimsky-plan-c-agentic-control-api.md
```

### Option 2 — automatic chaining

After Plan A completes, instead of stopping, the driving agent automatically invokes Plan B, then Plan C, halting only on the gate conditions below. The driving agent updates the execution log after each plan and each task.

**Who is the driving agent?** The Claude Code session that was running when `/subagent-dev` was first invoked on Plan A. After Plan A's final task, the agent reads this document, verifies Plan A's "definition of done" conditions, and invokes Plan B. Same for Plan C.

**To enable this:** the person kicking off Plan A explicitly passes "chain through B and C on success" as part of the initial instruction. Example:

```
/subagent-dev docs/plans/2026-04-21-rimsky-plan-a-foundation-scheduler.md

When Plan A's "Definition of done" conditions all verify green, proceed automatically to Plan B (docs/plans/2026-04-21-rimsky-plan-b-deterministic-supervisor.md) using the same subagent-driven pattern, then to Plan C (docs/plans/2026-04-21-rimsky-plan-c-agentic-control-api.md). Follow the gate and halt conditions in docs/plans/2026-04-21-rimsky-execution-chain.md. Update docs/plans/2026-04-21-rimsky-execution-log.md after each task and each plan transition.
```

## Gate conditions (must be green before advancing)

At each plan's "Definition of done" task:

1. `cd rimsky && npm run build` exits 0.
2. `npm test` exits 0 with all tests passing (Plan A's tests + Plan B's + Plan C's as applicable — later plans' suites include all prior plans' tests).
3. `npm run lint` exits 0.
4. The plan's additional verification steps (migration application, reference-binary startup for Plan C, etc.) succeed.
5. `CHANGELOG.md` (repo root) has the plan's entry appended.
6. The execution log has a `plan_completed` entry for the finishing plan.

If any gate fails after the cleanup-loop exhausts (see halt conditions), the chain stops.

## Halt conditions (stop the chain; do not advance)

Stop execution and wait for human review if **any** of the following occurs:

1. **Same task fails three subagent-cleanup cycles in a row.** One task consistently not passing review means a real issue the planning didn't anticipate. Do not paper over it with retry loops. Log the task, the most recent failure reason, and the three attempt summaries.

2. **A reviewer subagent reports a design-level concern** (as opposed to a bug or a buildability issue). E.g., "this interface is inconsistent with the spec in a way I cannot reconcile." Flag and halt.

3. **A test is flaky** (passes sometimes, fails sometimes). The expected response is to investigate and fix the flake, not to retry the test. If the root cause of flakiness cannot be identified after one focused attempt, halt.

4. **A fix touches files outside `/rimsky/`, `docs/`, or `CHANGELOG.md` / `feature-index.md`.** Rimsky v1 should not require edits to `/backend/`, `/frontend/`, `migrations/`, or anything in `/volumes/`. If a task wants to touch them, stop and surface the question.

5. **Destructive operations requested.** Any `rm -rf`, database drops outside rimsky's own schema, or overwriting untracked files — stop.

6. **Package-manager surprises.** A dependency fails to install, has a known CVE blocking the lockfile, or changes the package.json in ways not specified in the plan. Stop.

7. **Infrastructure failures unrelated to the code.** `testcontainers` cannot reach Docker, Postgres crashes mid-test, filesystem permissions. Log once, retry once; if still failing, halt.

## Guardrails (throughout)

1. **No git commits.** None. Plans explicitly do not include commit steps. You may use `git status` / `git diff` to check your work; you may not create commits unless the human explicitly requests it on their return.
2. **No pushes** of any kind (no `git push`, no `gh pr create`).
3. **No destructive git ops** (no `reset --hard`, no `checkout .`, no `clean -fd`, no branch deletion).
4. **No skipping hooks** (no `--no-verify` on any command).
5. **Scope discipline.** Edit only files enumerated in the current plan's task. If a task requires changing a file outside its enumeration, the task is underspecified — stop and log.
6. **Prefer small, reversible changes within each task.** A task's verification command must be runnable after each step, not only at the end.
7. **No branches.** Work on the current branch. Commits/branches are the user's decision.

## Execution log format

Maintain `docs/plans/2026-04-21-rimsky-execution-log.md` as an append-only log. Schema:

```markdown
# Rimsky v1 Execution Log

Updated live during unattended runs. Appends only; do not delete prior entries.

## Plan A — Foundation + Scheduler
**Started:** 2026-04-22 09:00 UTC
**Status:** in_progress | completed | halted

### Task 1.1 — Create /rimsky/ package metadata
- Started: 09:02
- Attempts: 1
- Outcome: completed
- Notes: none

### Task 1.2 — Create source directory skeleton
- Started: 09:05
- ...

(one subsection per task)

**Plan A completed:** 2026-04-22 14:30 UTC (or **Plan A halted:** ...)
**Gate check:** npm build ✓, npm test ✓ (187 tests), npm lint ✓, migrations applied ✓

---

## Plan B — Deterministic Supervisor
...
```

On halt, add a `## Halt: [reason]` section with full context (task, attempts, error output, relevant file paths).

## One-time setup before Day 1

Before the kickoff command runs:
- Confirm Docker is running (required for testcontainers in scenario tests).
- Confirm `node -v` shows 20.x or higher.
- Confirm `npm -v` works.
- Confirm working tree is clean (`git status` shows no uncommitted changes); otherwise note them in the log before starting.
- Confirm the three plan files and the spec file all exist at the paths listed above.

## When I (the human) return

1. Read the execution log top-to-bottom.
2. If all three plans completed: `cd rimsky && npm test` to confirm. Then review `git diff` (or `git status` — nothing should be committed). Commit what you want to keep.
3. If halted: read the halt section; decide on path forward (fix the blocker and resume, or revise the plan).
4. Clean up dev-only artifacts (e.g., the postgres docker container used in Task 3.3 of Plan A if it was left running).
