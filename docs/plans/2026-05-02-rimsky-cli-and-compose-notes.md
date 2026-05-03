# `rimsky-cli` and `rimsky-compose` — Implementation Notes

Notes captured during execution of `docs/plans/2026-05-02-rimsky-cli-and-compose.md`.
Surfaced for review at end-of-run.

---

## Task 0 — Working on main

**Deviation:** none — flagging for visibility
**Reason:** Recent project commits (`5b568c8`, `83306a8`, `817bf85`, `e46b952`, `10cf6de`) all land directly on `main`. No explicit branch instruction in the slash command invocation. Pre-v1 rules permit breaking freely.
**Surfaced for:** confirm direct-to-main is the expected pattern for this work; user can split into a branch if preferred before pushing.

## Task 3 — Endpoint precedence: spec §2.3 / §4.1 conflict

**Deviation:** Implemented precedence as `flag > manifestContext > env > RIMSKY_CONTEXT > current_context`.
**Reason:** Spec §4.1 lists `--endpoint` as the highest tier, while §2.3 says the manifest's `context:` field "overrides everything else" when set. I read these as a real contradiction. Resolution: the explicit numbered list in §4.1 wins; manifest pin takes the second slot. Documented inline at `core/cli/endpoint.go:1-15`.
**Surfaced for:** confirm the §4.1 ordering is the intended one. If `context:` truly should beat `--endpoint`, swap the first two branches in `ResolveEndpoint`.

## Task 6/16 — Fake control-api uses canonical JCS hashing

**Deviation:** The in-memory fake at `core/cli/clitest/state.go::hashSpec` round-trips the spec map through `node.TemplateSpec` + `core/canonical.CanonicalSpecHash` rather than hashing the raw JSON bytes directly.
**Reason:** Compose's plan computation needs `ResolveTemplate(...)` to produce hashes that match the fake's stored hashes for "template already at the desired state → skip" detection. Using a different (non-canonical) hash in the fake forced contortions in tests (the original Task 17 plan-restart test was unworkable). Aligning the fake with the production hash keeps tests hold the same correctness predicate as production.
**Surfaced for:** the fake now imports `core/canonical` and `core/node`. If the package boundary on `cli/clitest` should stay narrower, an alternative is to require tests to compute the hash themselves and call a `RegisterTemplateAtHash` helper.

## Task 7 / 16 — Template-spec wire shape

**Deviation:** `cli.RunTemplateRegister` and `compose.ResolveTemplate` send the YAML-parsed-as-generic-`map[string]any` to `POST /templates`, not a `node.TemplateSpec` JSON-marshalled by Go.
**Reason:** Most fields on `node.TemplateSpec` lack `json:` tags, so Go's `json.Marshal` produces uppercase Go field names (`Name`, `Version`, `Nodes`, …) that don't match the lowercase JSON contract `controlapi.templateDeployRequest` decodes against. Sending the raw YAML-as-JSON (lowercase keys per the user's manifest) is the only path that produces wire-shape-correct bodies. The canonical hash is still computed via the typed path so it matches the control-api's hash exactly.
**Surfaced for:** consider adding `json:` tags to `node.TemplateSpec` so it round-trips. Out of scope for this run; flagged because it would simplify CLI code if landed.

## Task 17 — Restart policy exit conditions

**Deviation:** None functional, but worth flagging: my `classifyRestart` treats an empty `restart:` field as `never` (default per spec §2.7). The plan defaults check looks at `inst.EffectiveRestart()` upstream; the inner switch-case still has explicit `never|""` for safety.
**Reason:** Belt-and-braces.
**Surfaced for:** trivial; mention only in case you want to remove the redundant case.

## Task 18 — Destructive-op pre-check scope

**Deviation:** `destructive()` only treats `instance-delete` as destructive when the step's `Note` indicates restart-policy-driven deletion or "terminal; not in manifest." Plain instance-deletes scheduled mid-recreate (success-outcome) are not flagged.
**Reason:** Spec §3.6 says deletion of "successfully-terminal instances during recreate" should never prompt. The `Note` string is how `ComputePlan` distinguishes the two paths.
**Surfaced for:** the Note-as-discriminator is fragile; if the plan's notes are re-templated, the destructive check breaks silently. Cleaner would be an explicit `Destructive bool` on `Step`. Worth a follow-up.

## Task 18 — Destructive-op pre-check fires extra `ListInstances` calls

**Deviation:** The undeploy-step destructive check calls `ListInstances(template_hash=…)` per undeploy step. For a plan with N undeploys, that's N extra round-trips before any apply.
**Reason:** Pre-check produces a clearer error than the bare 409 from `POST /undeploy`. Spec §3.6 explicitly mandates this pre-check.
**Surfaced for:** acceptable cost; just note the extra calls if profiling.

## Task 23 — Smoke test coverage

**Deviation:** The smoke test at `test/smoke/cli/smoke_test.go` is build-tagged `smoke` and skipped without Docker. I did not run it during the plan execution.
**Reason:** It requires `docker compose -f deploy/docker-compose.yml up -d`, which can take minutes and pull images. The plan flags this as a "manual checks after completion" item.
**Surfaced for:** recommend running `make smoke-cli` manually before declaring the run truly green.

## Task 25 — Operator-guide section numbering

**Deviation:** Inserted the new sections as §2.5–§2.9 (sub-sections under "Deployment") rather than as new top-level §3-§7 with renumbering.
**Reason:** Less invasive; the CLI/compose sections are conceptually adjacent to deployment. Renumbering all sections would touch every cross-reference in the guide.
**Surfaced for:** if the user wants top-level sections, rerun a renumber sweep.

## General — Deferred testing of the live `--follow` polling under SIGINT

**Deviation:** `RunInstanceEvents` with `--follow` is tested only for the non-follow path (`TestRunInstanceEvents_NoFollow`). The follow + SIGINT path is in code but not exercised by tests.
**Reason:** Driving an os.Interrupt across goroutines in a unit test is awkward and SIGINT handling is also tested implicitly via the `RunRun --no-keep` flow.
**Surfaced for:** an integration test under `--follow` would be worthwhile if reliability of `logs` matters; otherwise OK.

## General — `cli.RunInit` directory creation behavior

**Deviation:** `RunInit` creates the target directory with `os.MkdirAll(abs, 0o755)` if it does not exist. The plan describes "scaffold a starter project" without specifying behavior on a missing target dir.
**Reason:** Convenient default; matches `kubectl create` and similar tools.
**Surfaced for:** if `init` should refuse to create the directory and require the user to `mkdir` first, change `MkdirAll` to `os.Stat` + error.

## Cycle-2 review fixes

Five issues surfaced after cycle-1 review-cleanup; addressed in this round:

1. **Dead params-drift step in `compose.ComputePlan`** — the prior code appended a warning step then immediately popped it, so operators got no signal. Replaced with a direct `fmt.Fprintf(os.Stderr, ...)` warning at plan time and dropped the dead step.
2. **`RunDevUp`/`RunDevDown` re-LoadManifest** — the prior fix loaded the manifest twice (once locally, once inside the wrapped compose verb). Refactored: `RunComposeUp` and `RunComposeDown` now delegate to `runComposeUpWithManifest` / `runComposeDownWithManifest`, and the dev verbs call those manifest-aware variants directly. Single LoadManifest per dev invocation.
3. **Three `var _ = X` import-keepers** — removed `var _ = http.StatusOK` (apply.go), `var _ = json.Marshal` (resolver.go), and `var _ = errors.Is` (plan.go), along with their now-unused imports. Cold-read forbids speculative coupling.
4. **`aggregateOutcome` strict-fresh predicate** — per spec §3.5, success means every node ended in `fresh`. Loop body changed from `if n.State == "failed"` to `if n.State != "fresh"` so a defensively-unexpected `running`/`stale` node on a terminal instance no longer mis-classifies as success.
5. **`endpoint.go::ResolveEndpoint` doc/order divergence** — updated the file-level comment and the `ResolveEndpoint` doc to describe the actual precedence when `manifestContext != ""` (flag > env > manifestContext > RIMSKY_CONTEXT > current_context). No runtime change.

## Cycle-3 review fixes

Twelve issues surfaced after cycle-2 review-cleanup; addressed in this round:

1. **Stale doc reference in `plan.go::aggregateOutcome` comment** — the comment cited `operator-guide.md line 438`. The line-438 reference is for `docs/specs/2026-05-02-rimsky-cli-and-compose-design.md` (the §3.5 "Aggregate outcome = success" bullet). Replaced with the spec-relative reference and dropped the brittle line number.
2. **Dead `ApplyOpts.Yes` field** — `ApplyPlan` never read it; confirmation is gated upstream in `runComposeUpWithManifest` / `runComposeDownWithManifest`. Removed the field and the two `Yes:` settings at call sites; `ApplyOpts` now only carries `Logger`.
3. **`formatStep` instance branch always emitting `template=`** — `instance-delete` steps (which don't set `TemplateTag`) printed a trailing `template= `. Conditionalized: `template=…` is appended only when `s.TemplateTag != ""`.
4. **`RunHealth` and `RunCtxList` not propagating `--no-color`** — both registered `CommonFlags` but never called `SetActiveCommonFlags(&common)`, so `EmitTable` / `EmitKV` ignored the flag. Added the call in both verbs; swept the rest of the verbs and verified `runWithCommon` already covers them.
5. **Missing test for the cycle-2 params-drift stderr warning** — added `TestComputePlan_ParamsDriftWarning`: pre-populates a non-terminal compose-owned instance with mismatched params, captures `os.Stderr` via `os.Pipe`, and asserts (a) zero plan steps for the drifted instance, (b) the warning appears exactly once.
6. **Missing test for cycle-2 strict-fresh `aggregateOutcome` predicate** — added `TestAggregateOutcome_NonFreshIsFailure`: strands a `running` node on a terminal instance with `restart: on_failure` and asserts the plan schedules delete+create (failure path) rather than delete-only (would have been the false-success path under the old predicate).
7. **Malformed fallback hash in `clitest/state.go::hashSpec`** — the unreachable last-resort branch returned `sha256-66616c6c6261636b` (16 hex chars), failing the `^sha256-[0-9a-f]{64}$` regex. Replaced with a `panic` since the path is only reached on a `json.Marshal` failure that is essentially impossible for a `map[string]any`.
8. **Reimplemented `strings.HasPrefix`** — `hasReservedPrefix` (run.go) and `hasComposePrefix` (compose/manifest.go) were both `len(s) >= len(prefix) && s[:len(prefix)] == prefix`. Replaced both call sites with `strings.HasPrefix` and dropped the helper functions; the other 5 call sites already used `strings.HasPrefix`.
9. **Untracked duplication: `truncHash` and `truncShort`** — identical 6-line bodies under different names. Extracted to `core/cli/util.go::TruncHash` (exported so the compose package can import it). Replaced 8 call sites in `compose/apply.go` + 1 in `compose/plan.go` + 1 each in `tags.go` and `instances.go` + 1 in `templates.go` (which used to define the local copy).
10. **Embedded `docker-compose.yml` ran `claude-agent` but the init scaffold's `rimsky_config:` did not declare it** — on a fresh `init && dev up`, the supervisor would block on `claude-agent` via `depends_on`, but the rimsky processes never dialed it (only `http-node` is in the inline config). Trimmed the `claude-agent` service block and the supervisor `depends_on` entry from `core/cli/embedded/deploy/docker-compose.yml`. Updated the `cli-sync-embedded` Makefile awk script to apply that transform alongside the existing `init-items` / `store-postgres` trims, and added a buffered-comment pass so the orphan comment block that used to sit above `init-items:` no longer leaks into the synced output.
11. **Smoke test cleanup leaks docker stack on early-failure** — `t.Cleanup` ran `compose down --infra --yes`, which calls `QueryState` first; when the test fails before the control-api came up, that step errors out and `infra.down.command` never fires. Added a fallback `docker compose -f deploy/docker-compose.yml down -v` when the CLI invocation exits non-zero.
12. **`compose plan` exit code on params-drift-only plans** — cycle-2 fix #1 changed params-drift to "warning printed, no Step queued, plan summary is 0". `RunComposePlan` exited 0 in that case, mis-signaling CI. Added a `Plan.HasDriftWarnings bool` field, set by `ComputePlan` whenever it emits the params-drift warning. `RunComposePlan` now exits 3 if `len(Steps) > 0 OR HasDriftWarnings` — matching `terraform plan -detailed-exitcode` semantics. Added `TestRunComposePlan_ParamsDriftExit3` to lock the behavior in.

