# Rimsky Go Port — Unattended Execution Chain

Entry point for the unattended implementation run across Plans A → B → C → D. Read this first before invoking any `/subagent-dev` run.

## Artifacts

- **Spec**: `docs/specs/2026-04-23-rimsky-go-port-design.md`
- **TS reference** (executable sketch for the architecture; unchanged during port): `rimsky/src/`
- **Plan A** — Foundation: protocol, orchestrator core, inline-jsonb resource, stub executor
  `docs/plans/2026-04-23-rimsky-go-port-plan-a-foundation.md`
- **Plan B** — Reference Executors: `http-node` (Go) + `claude-agent` (TypeScript)
  `docs/plans/2026-04-23-rimsky-go-port-plan-b-executors.md`
- **Plan C** — Production Readiness: `external-sql` resource, Docker/Compose/Helm, conformance suite
  `docs/plans/2026-04-23-rimsky-go-port-plan-c-production.md`
- **Plan D** — Documentation
  `docs/plans/2026-04-23-rimsky-go-port-plan-d-cutover.md`
- **Execution log** (live, updated during runs):
  `docs/plans/2026-04-23-rimsky-go-port-execution-log.md`

Plans execute in strict order: A, then B, then C, then D. Each plan's Phase 0 (if present) amends prior plans with minor carry-forward; otherwise each plan builds strictly on what its predecessors produced. Each plan completes fully (all gate conditions green) before the next begins.

## How to kick off

### Option 1 — manual kickoff

```
/subagent-dev docs/plans/2026-04-23-rimsky-go-port-plan-a-foundation.md
```

When Plan A completes successfully, invoke each subsequent plan the same way:

```
/subagent-dev docs/plans/2026-04-23-rimsky-go-port-plan-b-executors.md
/subagent-dev docs/plans/2026-04-23-rimsky-go-port-plan-c-production.md
/subagent-dev docs/plans/2026-04-23-rimsky-go-port-plan-d-cutover.md
```

### Option 2 — automatic daisy-chain (recommended)

Kick off Plan A with an explicit instruction to chain through B, C, D on success:

```
/subagent-dev docs/plans/2026-04-23-rimsky-go-port-plan-a-foundation.md

When Plan A's "Definition of done" conditions all verify green, proceed automatically to Plan B (docs/plans/2026-04-23-rimsky-go-port-plan-b-executors.md) using the same subagent-driven pattern, then to Plan C (docs/plans/2026-04-23-rimsky-go-port-plan-c-production.md), then to Plan D (docs/plans/2026-04-23-rimsky-go-port-plan-d-cutover.md). Follow the gate and halt conditions in docs/plans/2026-04-23-rimsky-go-port-execution-chain.md. Update docs/plans/2026-04-23-rimsky-go-port-execution-log.md after each task and each plan transition.
```

**Who drives the chain?** The Claude Code session running when `/subagent-dev` was first invoked on Plan A. After Plan A's final task, the driver reads this document, verifies Plan A's Definition-of-done gates, and invokes Plan B. Same for C and D.

## Gate conditions (must be green before advancing)

At each plan's Definition of done:

1. `cd rimsky-go && go build ./...` exits 0.
2. `go test ./...` exits 0 with all tests passing (later plans' suites include all prior plans' tests).
3. `go vet ./...` exits 0.
4. `golangci-lint run` exits 0 (once golangci-lint is configured in Plan A).
5. **Migrations apply cleanly against a fresh Postgres.** (`rimsky-migrate` against a throwaway container; re-run is a no-op.)
6. **The plan's additional verification steps** (binary startup probes, scenario coverage, conformance runs, etc.) succeed.
7. **`rimsky-go/CHANGELOG.md`** has the plan's entry appended.
8. **The execution log** has a `plan_completed` entry for the finishing plan.

Plan C additionally requires:
- `docker compose -f deploy/docker-compose.yml up -d` brings the full reference stack online within 60 seconds; `docker compose ps` shows all services healthy.
- `rimsky-conformance` green against both reference executors in stub-mode (spec §14.4).

Plan D additionally requires:
- Every doc listed in spec §15 exists and is non-placeholder.
- All rimsky-project docs per spec §15 exist at their committed paths and are non-placeholder.

If any gate fails after the cleanup-loop exhausts (see halt conditions), the chain stops.

## Halt conditions (stop the chain; do not advance)

Stop execution and wait for human review if **any** of the following occurs:

1. **Same task fails three subagent-cleanup cycles in a row.** One task consistently failing review means a real design issue. Do not paper over it with retry loops. Log the task, the most recent failure reason, and the three attempt summaries.

2. **A reviewer subagent reports a design-level concern** (as opposed to a bug or a buildability issue). E.g., "this interface is inconsistent with the spec in a way I cannot reconcile." Flag and halt.

3. **A test is flaky** (passes sometimes, fails sometimes). The expected response is to investigate and fix the flake, not retry. If the root cause cannot be identified after one focused attempt, halt.

4. **A fix touches files outside `/rimsky-go/` or `/rimsky-go/docs/`.** In all plans, edits to `/backend/`, `/frontend/`, `/rimsky/` (the TS project), or anything under `/volumes/` are out of scope. Rimsky is an independent project; consumer-specific migration work lives in consumer-owned documentation, not here. If a task wants to touch files outside `rimsky-go/`, stop and surface the question.

5. **Destructive operations requested.** Any `rm -rf`, database drops outside rimsky's own schema, or overwriting untracked files — stop.

6. **Dependency surprises.** A Go module fails to resolve, has a known CVE blocking the lockfile, or changes go.mod in ways not specified in the plan. Stop.

7. **Infrastructure failures unrelated to the code.** `testcontainers-go` cannot reach Docker, Postgres crashes mid-test, filesystem permissions. Log once, retry once; if still failing, halt.

8. **Protocol incompatibility** — if a task generates breaking changes to `proto/v1/*` after Plan A has frozen the v1 proto surface. Halt and surface the design question.

## Guardrails (throughout)

1. **No git commits.** Plans explicitly do not include commit steps. You may use `git status`/`git diff` to check your work; you may not create commits unless the human explicitly requests it on their return.
2. **No pushes** (no `git push`, no `gh pr create`).
3. **No destructive git ops** (no `reset --hard`, no `checkout .`, no `clean -fd`, no branch deletion).
4. **No skipping hooks** (no `--no-verify`).
5. **Scope discipline.** Edit only files enumerated in the current plan's task. If a task requires changes outside its enumeration, the task is underspecified — stop and log.
6. **No edits to `/rimsky/` (the TS project).** The TS project stays in place unchanged throughout Plans A–D.
7. **The proto/v1 surface is frozen after Plan A.** Subsequent plans add new messages in new files, not modifications to frozen files.
8. **Prefer small, reversible changes.** A task's verification command must be runnable after each step, not only at the end.

## Execution log format

Maintain `docs/plans/2026-04-23-rimsky-go-port-execution-log.md` as append-only. Schema:

```markdown
# Rimsky Go Port Execution Log

Updated live during unattended runs. Appends only; do not delete prior entries.

## Plan A — Foundation
**Started:** YYYY-MM-DD HH:MM UTC
**Status:** in_progress | completed | halted

### Task 1.1 — [name]
- Started: HH:MM
- Attempts: 1
- Outcome: completed
- Notes: none

(one subsection per task)

**Plan A completed:** YYYY-MM-DD HH:MM UTC
**Gate check:** go build ✓ · go test ✓ (N tests) · go vet ✓ · lint ✓ · migrations ✓ · CHANGELOG ✓

---

## Plan B — Reference Executors
...
```

On halt, add a `## Halt: [reason]` section with full context (task, attempts, error output, relevant file paths).

## One-time setup before Plan A kickoff

Before the kickoff command runs:
- Confirm Docker is running (required for testcontainers-go in scenario tests).
- Confirm `go version` shows Go 1.22 or higher.
- Confirm `protoc` is installed (or that the plan's Phase 1 will install it via `go install` for the generator plugins).
- Confirm `docker compose version` works (needed by Plan C).
- Confirm working tree is clean (`git status` shows no uncommitted changes); otherwise note them in the log before starting.
- Confirm the four plan files, the spec, and this chain doc all exist at the paths listed above.

## When the human returns

1. Read the execution log top-to-bottom.
2. If all four plans completed: `cd rimsky-go && go test ./...` to confirm; `docker compose -f deploy/docker-compose.yml up -d` to confirm the stack starts. Review `git status`/`git diff` — nothing should be committed. Commit what you want to keep.
3. If halted: read the halt section; decide path forward (fix the blocker and resume, or revise the plan).
4. Clean up dev-only artifacts (stray testcontainers Postgres containers, docker-compose state if left up).
