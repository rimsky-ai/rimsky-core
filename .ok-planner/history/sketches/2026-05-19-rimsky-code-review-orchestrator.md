# Rimsky-orchestrated code review

Fusion of crimefinder's structural fan-out and the ok-planner `/review-holistic`
skill's design-doc steering, expressed as a rimsky graph. Demo target: prove
rimsky can host long-running, multi-agent, design-aware code-quality work
across one or many repos.

The skill version's failure modes are (a) one reviewer subagent's context
isn't big enough for a large codebase and (b) "agents love to find a handful
then stop" can't be cured by prompt diligence. Crimefinder fixes both
structurally — partition into zones, fan out one subagent per zone, persist
findings via MCP, gate stopping on coverage not novelty — but has no
design-doc awareness. This sketch is the fusion.

## What

A `concept:template` named `code-review-pass`. One instance per *review pass*.
The pass:

1. Reads the repo's design-doc catalog (`.ok-planner/design/concepts/` and
   `tensions/`) plus `@concept:` annotations across the code.
2. Partitions the codebase into zones (crimefinder's algorithm).
3. Fans out one `executor:claude-agent` per zone with a small MCP surface.
4. Persists findings to `.rimsky/findings.jsonl` in the repo working tree
   as durable git-tracked artifacts, classified by the `/review-holistic`
   five-class scheme. Rimsky Postgres holds only orchestration state.
5. Auto-fixes classes 1-4 through a bounded fix-cycle. Each fix commits
   the code change AND the JSONL status-update atomically.
6. Leaves class-5b findings (design-doc disagrees with code) as
   `status:open` JSONL rows, visible via a cascade-graph "review queue"
   route. Operator picks one up later via `/refine-design`; the resulting
   `/execute-plan` commit appends a status-update row that drops it from
   the queue. No second graph instance, no cross-instance messaging — the
   JSONL is the queue.

## Graph topology

```
template: code-review-pass
  params:
    repo_root         path
    review_mission    string  (default "convergence pass")
    fix_cycle_cap     int     (default 3; matches /review-holistic)

  graph: main

    discover-context
      Reads CLAUDE.md, .claude/rules/, design/concepts.md TOC,
      every design/concepts/<slug>.md, every design/tensions/<slug>.md,
      grep @concept: across the repo.
      Emits: attribute:context_manifest (inert JSON).

    partition-codebase
      Deterministic; mirrors crimefinder's partition.ts.
      Walks file tree, ignores standard patterns, splits dirs >max,
      merges small siblings. For each zone, computes concept_slugs
      from @concept: annotations in zone files plus catalog hints.
      Emits: attribute:zones (list of {id, label, files, concept_slugs}).

    review-fan-out  (concept:fan-out)
      One sub-claim per zone. Each sub-graph:

        review-zone  (executor:claude-agent, runs on host)
          --allowedTools: Read,Glob,Grep,Edit,Write,
                          mcp__rimsky_review__*
          (NO Bash — every side effect goes through a gate.)
          MCP gates (served by the host executor itself, see
          "Host executor topology" below): review_context,
          review_finding, review_coverage, review_complete,
          review_run_tests, review_commit_fix, review_defer,
          review_skip_zone, review_request_help.
          Reads zone files + concept docs for the zone's concept_slugs
          + open tensions touching those concepts. Reports findings
          via review_finding (class 1-5). The gate appends each
          finding to .rimsky/findings.jsonl AND emits a named-event
          back to rimsky for cascade. Reports visited files via
          review_coverage. Calls review_complete when done; gate
          errors if there are unresolved in-flight findings.

    aggregate-findings  (deterministic)
      Reads .rimsky/findings.jsonl rows where pass_id matches this run.
      Splits class-1-4 vs class-5.

    dedup-findings  (claude-agent fan-out, crimefinder pattern)
      Grouped by file. Appends "duplicate" status-update rows to
      findings.jsonl referencing the survivor's ID.

    branch:

      class-1-4-fix-cycle  (sub-graph, bounded loop)
        Up to fix_cycle_cap iterations:
          1. fix-fan-out — one fixer claude-agent per affected zone.
             Same host-executor as review-zone, just dispatched
             with a "fix" mission. Atomic-commit + JSONL-update is
             enforced structurally because review_commit_fix is the
             ONLY way the fixer can commit; gate validates
             finding-exists-and-open, test-pass (if policy on),
             working-tree-dirty before performing
             git-add + JSONL-append + git-commit atomically.
          2. re-review-affected — same review-zone shape, scoped to
             zones touched by fixers.
          3. settle — if no new class-1-4 findings, exit; else loop.
        Lifecycle-handler on fixer-complete fires re-review.

      class-5-finalize  (deterministic)
        For each class-5 finding: appends status:open to findings.jsonl
        (no-op if already there). No external emit — the JSONL row IS
        the queue. cascade-graph operator dashboard surfaces these via
        a /observability/review-queue route filtered to class:5,
        status:open.

    report
      Final summary appended to .rimsky/passes.jsonl: coverage map per
      zone, fix cycles run, class-5b items left open, class-5a items
      needing attention.
```

## Data model

**Findings live in the repo, not in rimsky Postgres.** Direct lift of
crimefinder's `issues.jsonl` ↔ `.crimefinder/issues.db` split: JSONL is
the durable, git-tracked, mergeable artifact; rimsky Postgres holds
operational orchestration state and doesn't carry finding-truth. This
inverts the original Postgres-as-source-of-truth assumption — single-repo
deployment + branch/merge semantics make JSONL the load-bearing primitive.

### In-repo, durable, committed

- `.rimsky/findings.jsonl` — append-only, one row per finding emit OR
  status update. Each finding has a nonce ID; status updates are new
  rows with a `ref` pointer to the original. Row shape:

  ```jsonl
  {"id":"f_a1b2c3","ts":"...","pass_id":"p_xyz","class":1,"file":"foo.go",
   "line":42,"description":"...","fingerprint":"sha256...","status":"open",
   "concept_slug":null,"tension_slug":null,"confidence":"high"}
  {"id":"f_a1b2c3_u1","ts":"...","ref":"f_a1b2c3","status":"fixed",
   "by_pass":"p_xyz","note":"fix-cycle iter 1"}
  ```

  Status materialization is "last-write-wins by ts among rows sharing ref."
  The nonce IDs make git merge trivial: every row is independent, conflicts
  only happen on byte-equal rows which never occur in practice.

- `.rimsky/passes.jsonl` — append-only, one row per review-pass summary
  (started_at, ended_at, mission, exit_reason, findings_emitted,
  coverage_pct).

- `.rimsky/coverage.jsonl` — optional, per-pass coverage entries. Could
  be folded into the pass summary if granularity at file-level isn't
  worth the row volume.

The `fingerprint` field (sha256 of normalized file+description, line-agnostic
per crimefinder's pattern) is what powers re-discovery dedup across passes
and branches.

### In rimsky Postgres, operational, not committed

Existing tables, no new schema needed for findings themselves:

- `concept:instance` (`rimsky_instances`) — one row per review-pass instance,
  `params` carries `{repo_root, mission, pass_id}`.
- `concept:node-run` (`rimsky_node_runs`) — per-zone executions during a pass.
- `concept:claim-handle` (`rimsky_claim_handles`) — zone claims, fixer claims.
- `concept:event-log` (`rimsky_events`) — full audit trail of the pass.
- `concept:frame` (`rimsky_frames`) — cascade frames for the pass.

Nukeable. If Postgres is wiped between passes (dev, debugging, redeploy),
JSONL is intact and the next pass starts fresh against it.

### What flows between the two

The MCP tools (`review_finding`, `review_coverage`, `review_complete`)
serve as the seam: an executor calling `review_finding` produces both a
`concept:named-event` row in rimsky's event-log (for orchestration
cascade) AND a `.rimsky/findings.jsonl` append (for durable truth). The
MCP shim on control-api does both writes; the JSONL append goes through
the file API into the repo working tree.

### Branch/merge semantics

JSONL appends with unique nonce IDs merge cleanly with `git merge`. The
dedup phase on the *next* review-pass reconciles re-discoveries via the
`fingerprint` field. "Which commit fixed F1?" is `cmd:git log -p
.rimsky/findings.jsonl` filtered to lines referencing F1.

Edge cases:

- **Rimsky Postgres dies mid-pass.** Findings already in JSONL stay
  (incremental MCP writes, not batched-on-session-end). Orchestration
  state for the dead pass is lost. Next pass dedups against partial set.
  No special reconciliation logic.
- **Two branches fix the same finding differently.** Each branch appends
  its own `status:fixed` update row. On merge both survive; the conflict
  is visible to a human reviewing the JSONL but rimsky doesn't try to
  arbitrate. Rare; surfacing is enough.
- **Re-discovery.** Fingerprint match → no new finding row, optional
  `confirmation` update row (bumps seen-count, signal for prioritization).

## MCP tool surface (gates served by the host executor)

The MCP server for `review_*` tools lives in the **host claude-agent
executor**, not on `concept:control-api`. The gates have to perform
host-side side effects (git commit, JSONL append, test invocation), so
they sit on the host where those operations naturally belong. See the
"Host executor topology" section below for why.

Every gate is a typed state-transition with required metadata and
deterministic validation. The agent runs without `Bash`; its
`--allowedTools` is `Read,Glob,Grep,Edit,Write,mcp__rimsky_review__*`.
Working-tree edits go through `Edit`/`Write`; anything with a side
effect beyond the working tree goes through a gate. The gates are the
only way for the agent to commit code, run tests, defer findings, or
mark a zone complete. The agent's behavior becomes a state machine
enforced by tool ordering — diligence is structural, not prompted.

The gates:

- `review_context(run_id, zone_id)`
  → `{mission, zone_files, concept_docs[], open_tensions[]}`.
  Server-side: looks up the zone, fetches concept files for
  `zone.concept_slugs`, fetches open tensions referencing those concepts,
  returns inline. Concept-aware crime_context.

- `review_finding(class, file, line_range, description, concept_slug?,
  tension_slug?, confidence)`
  → Appends a finding row to `.rimsky/findings.jsonl` AND emits a
  `concept:named-event` on the calling review-zone node.
  **Server-side validation enforces the /review-holistic discipline structurally:**
    - A class-1-4 finding whose `concept_slug` references a concept whose
      Boundaries/Invariants the description doesn't quote → auto-routed
      to class-5b before write (rewrites the row before append).
    - A finding whose `tension_slug` matches an open tension → written
      as a `tension-confirmation` row, not as a fresh class-1-4 finding.
  JSONL appends are serialized through the MCP shim (single-writer on
  control-api) so parallel zone executors don't race on the file.

- `review_coverage(files_read[])`
  → Appends coverage rows (`.rimsky/coverage.jsonl` or folded into the
  pass summary). Incremental during the session so coverage survives
  session crash.

- `review_complete()`
  → Closes a review-zone session. Errors if there are findings the
  agent marked `status:fixing` but neither resolved (via
  `review_commit_fix`) nor explicitly deferred (via `review_defer`).
  Forces the agent to account for everything it claimed it was working
  on. Marks the review-zone node done; cascade fires on
  aggregate-findings.

- `review_run_tests()`
  → Runs the project's test command per `.rimsky/executor-config.yml`
  (`tests: 'go test ./...'`, `npm test`, etc.). Returns
  `{exit_code, output, ran_at, command_sha}`. Result is cached on the
  executor for the current session so `review_commit_fix` can check it.

- `review_commit_fix(finding_id, fix_description, commit_message)`
  → **The only way for the agent to commit.** Validates: finding
  exists and is in `status:open`/`status:fixing`; working tree has
  changes (`git status --porcelain` nonempty); if
  `require_tests_before_commit: true` in executor-config, the most
  recent `review_run_tests()` returned exit 0 against the current
  working tree (cache invalidates on file change). Then: `git add` of
  changed paths, append `{ref:finding_id, status:fixed, by_pass,
  resolved_at_commit:<sha>}` to `.rimsky/findings.jsonl`, then
  `git commit -m <commit_message>` with a `Resolves: finding_id`
  footer. Returns commit SHA. The atomic-fix-commit-plus-JSONL-update
  problem from the prior open-questions list disappears: the agent
  literally cannot commit any other way.

- `review_defer(finding_id, reason)`
  → Required to skip a finding without fixing it. Appends
  `{ref:finding_id, status:deferred, reason}` to findings.jsonl.

- `review_skip_zone(reason)`
  → Required to close a zone with low coverage. Recorded on the pass
  summary in passes.jsonl. Without this, `review_complete()` will
  reject closure if coverage is below threshold.

- `review_request_help(question, blocker_finding_id?)`
  → Explicit escape hatch when the agent is stuck. Surfaces to the
  cascade-graph operator dashboard. Better than the agent silently
  giving up; turns "I don't know what to do" into a visible, durable
  item rather than a quiet session-end.

The server-side class-5b auto-routing inside `review_finding` is the
load-bearing piece for design-doc discipline. The atomic-commit gate
is the load-bearing piece for fix-cycle correctness. Together they
turn most "be diligent about X" prompt instructions into structural
validation — agent failure modes (find-handful-and-stop, fake-test-pass,
commit-without-updating-findings) collapse into "the gate refuses."

## Host executor topology

Rimsky lives in Docker; the coding agent has to run on the dev machine
(Claude CLI installed there, repo working tree there, host git there).
Docker-in-docker is the wrong shape. The clean answer uses what
rimsky already has: `dir:executors/claude-agent/` implements the gRPC
`concept:executor` protocol — run it as a host process, not inside
the rimsky compose stack.

```
[Dev machine host]                       [rimsky compose stack]

claude-agent executor (Node process)     rimsky-supervisor
  bound 127.0.0.1:7000 (gRPC)              cfg:executors.claude-agent.endpoint =
                                             "host.docker.internal:7000"
  on Executor.Execute(dispatch):
    1. spawn Claude CLI subprocess         control-api
       --cwd <repo_root from params>         (no review-* MCP shim;
       --mcp-config <generated tempfile>     it's on the host now)
       --allowedTools Read,Glob,Grep,
                      Edit,Write,            postgres
                      mcp__rimsky_review__*     (orchestration state;
    2. host an in-process MCP server          findings live in the repo)
       Claude CLI dials; serves all
       review_* gates above; owns the
       host-side side effects.
    3. stream concept:named-event rows
       + executor terminal back to
       rimsky over the gRPC executor
       protocol.
```

The executor is the **gatekeeper**. Rimsky never reaches into the host;
the executor performs every side effect and reports back via
`concept:named-event` over the gRPC executor protocol. The control plane
(frames, claim-handles, event-log) stays in rimsky; the data plane (git,
JSONL, tests) stays on the host.

Connectivity:
- macOS / Docker Desktop: `host.docker.internal:7000` resolves from the
  rimsky container to the host loopback. No special config.
- Linux: `--add-host=host.docker.internal:host-gateway` in
  `file:deploy/docker-compose.yml`, or use the bridge IP.
- Authentication: executor binds 127.0.0.1 only (host-local), plus a
  shared bearer token reused from rimsky's `concept:api-key` machinery.
  Defense in depth is cheap.

Per-repo deployment shape:
- One executor process per repo, on its own port (e.g. 7000 for repo A,
  7001 for repo B if two repos coexist on one dev machine).
- `cmd:rimsky up` brings up the Docker stack AND starts the host
  executor as a launchd/systemd unit (or foreground subprocess);
  `cmd:rimsky down` tears both down. Operator never starts them
  independently.
- `.rimsky/executor-config.yml` declares per-repo executor params:
  test command, allowed-tools override, gate policies
  (`require_tests_before_commit: true`, coverage thresholds). Lives
  in the repo, committed.

## Class-5b "queue" is a JSONL query, not a graph instance

The original sketch had `class-5-emit` POST to a long-lived
`template:design-reconciliation` instance via publisher messaging.
That dissolves under the JSONL-as-truth model: the "queue" is just

```
jq 'select(.class==5 and .status=="open")' .rimsky/findings.jsonl
```

served as a cascade-graph route (e.g.
`route:GET /observability/review-queue`). No second graph instance to
keep alive, no cross-instance message-idempotency, no async-callback
plumbing.

Workflow:

1. `class-5-emit` writes the JSONL row with `class:5b`, `status:open`,
   and the relevant `concept_slug`. Nothing else.
2. The operator dashboard surfaces open class-5b rows from the JSONL.
3. Operator picks one, runs `/refine-design` against the repo from
   inside Claude Code. The skill produces a spec; `/execute-plan` runs
   it; mutates the design docs per the existing rules.
4. The plan-execution commit includes a JSONL status-update row:
   `{ref:f_xxx, status:queued-to-spec, spec_path:".ok-planner/specs/..."}`.
   When the spec lands and `/execute-plan` finishes, another row:
   `{ref:f_xxx, status:resolved, resolved_at_commit:"sha..."}`.
5. The row drops off the queue.

Trade-off acknowledged: the cross-instance `concept:publisher-subscription`
demo point goes away in this direction. We still exercise the protocol
at the ingress boundary — see Triggers below — so the primitive is
still demonstrated, just at "graph receives messages from sensors"
rather than "graph emits messages to graph."

Demo payoff: a finding surfaced by an autonomous review pass becomes a
durable, git-tracked, branch-portable design-reconciliation item.
Humans triage at their own pace via the same `/refine-design` skill
they already use. The loop closes when the design-doc change ships
and the JSONL records the resolution commit. Rimsky owns dispatch,
fan-out, claim-recovery; the repo owns the truth.

## Triggers

Four launch surfaces, all expressed as `concept:publisher` implementations
(no new primitives):

1. **Manual.** `cmd:rimsky review-run --repo <path> [--mission ...]`.
   Thin CLI wrapper that POSTs to control-api to create a
   `template:code-review-pass` instance.
2. **Cron.** `sensor:sensor-cron` fires a review-run instance on schedule
   per registered repo. Weekly or monthly convergence pass.
3. **Git event.** `sensor:sensor-webhook` subscribed to a repo's
   post-merge-to-main hook fires a review-run scoped to the changed
   files (zones constructed from the diff, not the full tree).
4. **Concept-doc edit.** A separate webhook on
   `.ok-planner/design/concepts/*.md` fires a focused review-run
   re-checking the codebase against the changed concept's
   Boundaries/Invariants. The "concept changed, find the drift" use case
   — inherently cross-temporal, exactly what the skill couldn't do.

## What this exercises in rimsky (proof-of-platform value)

- `concept:fan-out` with bounded sub-claims, real concurrent claude-agent
  dispatch under one supervisor.
- `concept:claim-handle` lifecycle: each `review-zone` claims its zone,
  fix-cycle fixers claim affected zones, `concept:claim-tree` retention
  proves crash recovery survives mid-pass restart.
- `concept:publisher-subscription` with idempotency: exercised at the
  *ingress* boundary (sensor-cron, sensor-webhook firing review-pass
  instances), not as a cross-graph bridge. The class-5b "queue" is a
  JSONL query so no second instance needs a subscription.
- `concept:event-log` as the orchestration audit trail; findings live
  in the repo's JSONL, but every dispatch / claim / state-transition
  flows through `rimsky_events` for forensic reconstruction.
- `concept:lifecycle-handler` on the fix-cycle: re-review-on-fixer-complete
  is the natural lifecycle-handler use case.
- `concept:executor` (claude-agent) and `concept:validation` for MCP-tool
  argument validation at registration time.
- Single-repo per rimsky deployment is the target shape; the user's
  invariant (one rimsky stack per repo) keeps concept slugs and JSONL
  paths unambiguous and removes the multi-repo coordination question
  from scope.

## Open questions / tensions

(Things worth pinning before committing to a plan.)

- **JSONL append concurrency within a pass.** Multiple zone agents
  running in parallel all want to append to `.rimsky/findings.jsonl`.
  With the gates on the host executor, single-writer is the natural
  shape — the executor process serializes JSONL writes through an
  internal mutex. Per-zone shard files merged at pass-end is the
  fallback if throughput becomes an issue. Probably the mutex is fine;
  finding emit rate is low.
- **One executor per repo on a multi-repo dev machine.** If the dev
  works on multiple repos with rimsky-orchestrated review on each,
  they run N executor processes on N ports, each paired with its own
  rimsky-stack-in-docker. `cmd:rimsky up` handles the pairing per
  repo. Cross-repo isolation falls out for free — each executor only
  knows about its own `repo_root`.
- **Auth on the executor's gRPC listener.** Bind 127.0.0.1 only +
  shared bearer token from rimsky's `concept:api-key` machinery.
  Host-loopback alone is sufficient; the token is defense-in-depth.
- **Executor crash mid-fix.** If the executor process dies after a
  `review_commit_fix` has run `git commit` but before it can report
  the named-event back to rimsky, rimsky's `concept:claim-handle` for
  that zone is orphaned. The `concept:orphan-reaper` sweep cleans up
  the rimsky-side state; the JSONL and the commit are intact on the
  host (commit and JSONL append happen synchronously inside the gate).
  Next pass dedups against the JSONL. No reconciliation logic needed.
- **Test-gating policy default.** `require_tests_before_commit: true`
  is the safe default; repos without tests set it to `false`. Worth
  declaring in a default `.rimsky/executor-config.yml` that
  `rimsky init` writes.
- **Cross-zone findings.** `/review-holistic` allows the reviewer to
  "follow refs outside your zone." Crimefinder's zone-boundary is hard.
  Fused version needs a rule: can zone-N file findings against zone-M's
  files? Lean yes; coverage counts under zone-M (where the file lives);
  the finding records originating-zone for forensics. Worth a tension
  entry pre-build.
- **Concept-annotation drift.** `/review-holistic`'s "annotate-on-consult"
  rule: when a reviewer consults a concept but the code site lacks
  `@concept:`, the fixer adds the annotation. In the graph that's a
  separate fix kind, routed through the same fix-cycle but with a
  smaller edit surface (annotation insertion only). Same atomic-commit
  rule applies: the annotation insertion and the JSONL update ship
  together.
- **Tension-already-cataloged.** Skill drops these as re-litigation.
  Better: persist as `tension-confirmation` JSONL rows so we track how
  often a known tension surfaces — signal for prioritizing resolution.
  Cheap, useful data, no downside.
- **Convergence stopping criterion.** Crimefinder: all zones covered.
  `/review-holistic`: zero findings. Fused: all zones covered AND last
  fix-cycle produced no new class-1-4 findings. `fix_cycle_cap` is the
  back-stop.
- **Class-5a operator surface.** These need human judgment but aren't
  design-doc changes — they're "fix or defer" calls. Lives on the same
  cascade-graph route as the class-5b queue, filtered. JSONL rows
  carry the distinction.
- **What does Postgres-reset cost in forensics?** JSONL has the findings
  truth; `rimsky_events` has the orchestration timeline. If Postgres is
  wiped, you lose the timeline of *how* the pass ran (which executor,
  which fan-out, which retries) but not *what* it produced. Acceptable
  trade for the simpler model.
- **End-to-end demo.** Register rimsky against the rimsky repo itself,
  cron a weekly review-pass, watch findings flow into `.rimsky/findings.jsonl`,
  triage one class-5b through `/refine-design`, watch the resulting spec
  land, watch the JSONL row close on merge. Dogfood, in-repo, no
  external bug-tracker dependency.

## Out of scope

- **Fixer's edit-applying surface.** The plan-skill `/execute-plan`
  already does this. Open question: does the rimsky fix-cycle reuse
  ok-planner-via-MCP, or invoke a thinner edit-only claude-agent?
  Punt; sketch only.
- **Frontend for the planning-queue.** Lives on cascade-graph routes
  initially; pretty UI later.
- **Cross-project concept reconciliation.** When two consumer repos
  share a concept that drifts in opposite directions. Out of scope
  until we have multi-repo running and the question is actually live.
