# Crimefinder Changelog

## Unreleased

- Real-rimsky integration harness added under `test/integration/`. The
  harness spawns rimsky-migrate (once), rimsky-control-api,
  rimsky-scheduler, rimsky-supervisor as host subprocesses against a tmp
  sqlite db, plus crimefinder-producer and crimefinder-executor (stub
  mode) as node subprocesses, picks free ports per-run, writes a fresh
  rimsky.yml + supervisor.yml, and exposes a small driver API:
  `registerTemplate`, `deployTemplate`, `createInstance`,
  `waitForInstanceTerminal`, `getInstance`, `readFindings/Passes/Coverage`,
  `gitLogMessages`, `teardown`. A new scenario
  `test/integration/full-pass.test.ts` exercises the wire surface
  (template parser, control-api HTTP, capabilities handshake, JSONL
  substrate) against a small fixture under `test/integration/fixtures/`.
  Gated behind `npm run test:integration`; NOT included in the default
  `npm test`. The harness fails fast with actionable build instructions
  when the required `bin/rimsky-*` Go binaries or workspace `dist/`
  outputs are missing.
- `templates/code-review-pass.yml` restructured so it actually validates
  against the current rimsky template parser:
  1. The legacy top-level `nodes:` block was wrapped in
     `graphs[name=main]`. The previous mixed shape (top-level `nodes:`
     plus a sibling `graphs:` block) tripped rimsky's
     `graphs_and_nodes_both_set` rejection — a real bug surfaced by the
     new integration harness.
  2. Every `stores[].name` is now `crimefinder` (the claim-producer
     declared in rimsky.yml), with a per-node `alias:` carrying the
     previous friendly handle (`pass-state`, `context-scan`, etc.) so
     the `{{claim.<alias>.payload.*}}` references in `attributes`
     resolve unchanged. Without explicit aliases the rimsky validator
     rejects with `unknown store "<alias>"`.
  3. The fix-iteration sub-graph's `fix-fan-out`, `re-review-affected`,
     and `iter-aggregate` nodes dropped their `holds: pass-state: { from:
     iter-guard }` declarations — `iter-guard` doesn't itself open the
     `pass-state` claim and rimsky's `holds_unknown_claim_alias` check
     fires accordingly. `pass_id` already flows through the existing
     `{{nodes.iter-guard.attribute.pass_id}}` substitutions, so the
     functional contract is unchanged.
  4. `fix-fan-out` now has an explicit `subscribes: [iter-guard]` edge
     so the sub-graph's reachability check (`subgraph_disconnected_internal_node`)
     passes.
  5. `templates/validate.mjs` updated to skip the `entry`/`exit` check
     on the `main` graph (rimsky's canonicalizer rejects
     `subgraph_main_has_entry_or_exit` when present there).
- `executor/src/cli-runner.ts` aligned with `executors/claude-agent/src/cli-runner.ts`:
  the rendered system prompt now writes to a chmod-0600 tmpfile under
  `os.tmpdir()` and is passed as `--system-prompt-file <path>` instead
  of `--system-prompt <inline>`. Tmpfile cleanup runs on subprocess
  `close` and `error`. Keeps the longer prompt out of `ps` and sidesteps
  OS-level argv length limits as templates grow. New
  `buildClaudeCliArgs` helper exported for arg-composition unit tests;
  the existing `cli-runner_test.ts` was updated to assert the new flag
  shape.
- Fix-cycle dispatch no longer leaks 5a/5b/fixed/deferred findings into the
  agent's `assigned_findings` payload. `splitAffected` now drops zones with
  empty fix buckets from the dispatch set (no agent spawned for a zone with
  nothing to fix), and `handleGetReviewContext` treats an empty
  `assigned_finding_ids` list as "no work" instead of falling back to
  "every finding in this zone". Re-review still dispatches all affected
  zones — re-review children don't carry per-zone IDs.
- Dead `findings_by_zone` removed from `openFixPartition`'s payload. After
  the address-byte-threading rewrite, no consumer read it; the per-zone
  IDs are materialized exclusively inside `splitAffected` for the children
  that need them.
- Startup recovery's last-wins ordering for `zone_plan` / `dedup_batches`
  rows now uses a row-position tiebreaker after `seq` and `ts`, so two
  legacy rows (no `seq`) sharing an identical ms-granularity `ts` still
  resolve deterministically to the most-recently-appended row.
- Fix-cycle / re-review dispatch wiring rewritten to use address bytes
  (not userdata) for per-child `iter_num` and `assigned_finding_ids`,
  honoring rimsky's userdata-opacity invariant. (Post-collapse note: the
  `runtime/userdata_overrides.go` file referenced below was renamed to
  `runtime/attribute_overrides.go` when the 2026-05-21 userdata collapse
  unified `userdata` into `attributes`; `graph/attribute/doc.go`'s
  former invariant 11 was retired with that change.) `runtime/userdata_overrides.go`
  is deep-merge-only; `graph/attribute/doc.go` §invariant 11 forbids
  substitution inside userdata. `splitAffected` now projects the cached
  `findings_by_zone` map onto each per-zone scope identity; `openFanOutChild`
  threads the values into the source-tree-zone address; the executor reads
  them off `primary` rather than from userdata. The template's broken
  `{{...}}` substitutions inside userdata are removed.
- `handleGetZoneCoverage` now applies the configured coverage threshold
  uniformly to every zone (via `computeZoneCoverage`), not just the
  calling session's. A zone with one file read out of twenty no longer
  registers as "done" and trips a premature `pass_closed`.
- `pass_closed` named-event is de-duplicated through a new
  `pass_closed_emitted` JSONL row (passes.jsonl). The producer's
  `JsonlStore.tryClaimPassClosedEmission` performs the atomic
  check-then-append under the passes-file mutex; only the first writer
  observes `pass_complete:true`, so concurrent zone completions can't
  emit duplicate `pass_closed` events.
- `pass_closed` payload now carries the same shape as the canonical
  `pass_finished` JSONL row (zones planned/completed/skipped, findings
  emitted/resolved/deferred, coverage_pct). The producer assembles a
  `PassSummary` and ships it on `GetZoneCoverageResponse.pass_summary_json`;
  `review_complete` stamps it onto the emitted event.
- `aggregate-findings.dedup_file_groups` no longer grows a full
  per-file map under high finding counts. The reducer keeps two maps
  (`seenOnce` / `seenMulti`) and only the multi-occurrence files are
  retained.
- `zone_plan` and `dedup_batches` JSONL rows now carry a per-pass
  monotonic `seq` field assigned under the passes-file mutex. Recovery
  uses `seq` for last-wins ordering instead of the ISO-8601 `ts` field,
  which tied under millisecond-level write throughput and resolved in
  map-iteration order. Legacy rows without `seq` fall back to `ts`.
- Initial implementation per spec `.ok-planner/specs/2026-05-19-crimefinder-design.md`.
  Custom rimsky executor + claim-producer + template YAML + CLI wrapper.
  Read-and-fix review pass with atomic commit-fix gate, class-5b auto-routing,
  and concept-doc-aware classification.
- Internal MCP server migrated from the deprecated `mcp.tool(...)` overloads
  to `mcp.registerTool(name, { description, inputSchema }, cb)` (matches the
  current `@modelcontextprotocol/sdk` surface). Behaviour unchanged.
- Code-review pass against the initial implementation: closed 42 spec-deviation
  and code-quality issues. Notable behaviour changes:
  - `review_complete` now enforces `coverage_below_threshold` per spec —
    rejects when zone coverage is below `cfg:coverage.threshold_pct` and the
    session hasn't recorded a `review_skip_zone`. New `GetZoneCoverage` RPC
    on the producer feeds the gate.
  - `review_context` returns the full role-polymorphic `ContextPayload`
    (concept docs, open tensions, finding-categories help, assigned findings
    for fix-cycle, etc.) via a new `GetReviewContext` RPC.
  - New `review_dedup_mark` gate + `MarkDuplicate` RPC: dedup sessions can
    now actually mark duplicates; cross-batch conflict resolution is wired
    in via `applyDedupResults`.
  - MCP server moves to header-based bearer-token auth (the per-tool
    `token` field is removed; the executor's MCP-config injects an
    `Authorization: Bearer …` header).
  - Session-tokens persist to `.crimefinder/tokens.jsonl` with a 24h TTL
    so a producer restart doesn't invalidate in-flight sessions.
  - Producer adds role-checks on `review_coverage` (review-zone only,
    with file-existence + path-traversal rejection) and `review_run_tests`
    (not for dedup sessions).
  - `IterationCounter.nextFor` is idempotent against retried `Open`
    requests (the `claim_id` is recorded on the `iter_marker` row) and
    serializes concurrent callers under a per-pass lock.
  - `source-tree` parent claim returns a `no-op` address (new shape) —
    the fan-out parent has no typed-state surface, only the per-zone
    sub-claims do.
  - Dedup batch sub-claims now carry a distinct `dedup-batch` scope
    identity + address rather than reusing `source-tree-zone`.
  - Startup recovery scans git since the most-recent known
    `resolved_at_commit` instead of the last 500 commits.
  - `iter-aggregate.findings_resolved_this_iter` filters by the iter's
    timestamp window (between adjacent `iter_marker` rows).
  - Class-5b auto-routing now also triggers when the cited concept slug
    points at a missing doc.
  - `silence-watch` resets on every authenticated MCP tool call (not just
    on stdout) so long agent runs that don't write stdout don't false-fire.
  - Health endpoint stops writing `.health-touch` every probe.
  - `git-ops.mtime` is now an O(dirty-paths) operation instead of
    statting every tracked file.
  - `crimefinder status` queries `rimsky instance list` for live passes
    in addition to walking the JSONL history.
- Second-round code-review pass: closed 25 follow-up issues:
  - **Role on every session-token** — `ZoneScopeIdentitySchema` carries
    a new `role` field that `SplitScope` sets per partition kind
    (review-zone / fix-cycle / re-review). `openFanOutChild` reads it
    and tags the issued session-token, so the four role-guarded gates
    (`review_skip_zone`, `review_finding`, `review_commit_fix`,
    `review_defer`) and the role-polymorphic `review_context` payload
    can actually distinguish dispatch missions in production.
  - **Dedup `review_context` returns `file_groups`** — the session-token
    now carries `batchIndex`; `handleGetReviewContext` looks the batch
    up in `partitionCache.getDedupBatches` and projects to
    `{file, finding_ids}`. Closes the shared-schema / prompt /
    runtime mismatch.
  - **Empty-fan-out terminal** — when a fix-cycle / re-review dispatch
    arrives with no source-tree-zone primary (or only a `pass-state`
    primary), `runAgent` returns `{success, changed:false}` instead of
    `tool_error`. Matches spec lines 836-841.
  - **Session-token TTL is enforced at `validate()`**, not just at
    `reload()`. Registry stamps issue time itself so unit tests passing
    `issuedAt: 0` still work.
  - **`review_skip_zone` rejects when coverage already meets threshold**,
    so an agent can't short-circuit a fully-readable zone.
  - **`IterationCounter` per-pass queue cleanup actually fires** — the
    tail Promise is now stored in a local before reference-equality
    check, so the map doesn't grow monotonically.
  - **`UpdateFindingStatus` / `MarkDuplicate` use `GateError`** with
    new error classes (`invalid_status`, `invalid_request`) instead of
    plain `Error`, so the MCP envelope carries a typed error class.
  - **`Commit` revokes the bound session-token** like `Abandon` /
    `Release`.
  - **All twelve named events are declared** (per spec lines 584-595).
    `pass_opened`, `zone_started`, `finding_dedup_marked` are emitted
    at runtime; `pass_closed` remains declared.
  - **`review_finding` returns `crimefinder_error_classes` array**
    instead of a scalar `crimefinder_error_class`, so the class-5b
    reroute and tension-confirmation signals can't overwrite each
    other.
  - **`userdataSchema` declares the fields executor reads**:
    `coverage_threshold_pct`, `coverage_on_below_threshold`, `iter_num`,
    `trigger`.
  - **Executor reads `.crimefinder/config.yml` directly** for coverage
    knobs (`coverage_threshold_pct`, `coverage_on_below_threshold`) —
    the template DSL can't substitute nested cfg into per-node userdata.
  - **Coverage-threshold scenario test** added: drives the gate
    branching against real producer state (with-and-without
    `review_skip_zone`).
  - **`coverage_file_escaped` is its own error class**, distinct from
    `coverage_file_missing` — path-traversal attempts now get logged
    at `warn` so the security signal isn't lost.
  - **Zone plan persists to `passes.jsonl`** as a new `zone_plan` row
    kind. Producer crash mid-pass rehydrates the SAME zone IDs at
    startup, so post-restart findings keep stable `zone_id` rather
    than drifting toward `z_unknown`.
  - **`internal-mcp-server.registerTools` iterates `TOOL_DEFINITIONS`**
    — single source of truth for descriptions/schemas.
  - **`fix-partition` / `re-review-partition` / `dedup-grouping`
    parents return a `no-op` address** (mirrors `source-tree`).
    Removes the dead session-token issuance from parent claims.
  - **`git-ops.log` falls back to bounded recent-history** when
    `sinceSha` is unreachable (rebase/force-push), instead of
    silently returning `[]`.
  - **`dedup-batch` payload's `file_count` defensively handles
    missing `files`** array.
  - **`agent-run` passes `runId` explicitly** (separate from
    `dispatchId`) into the MCP server, matching the
    `server.ts::runId` semantics.
  - **`fix_cycle_iterations_run` counted from commits**, not
    raw `iter_marker` rows — skipped iterations no longer inflate
    the report.
- Third-round code-review pass: closed 9 follow-up issues:
  - **`pass_closed` is now actually emitted**. `GetZoneCoverage`
    returns a new `pass_complete` flag (true when every zone in the
    pass has either coverage or a `skip_zone` row after this zone's
    completion); `review_complete` emits `pass_closed` when set.
    Best-effort — the canonical signal stays the `pass_finished`
    JSONL row.
  - **`coverage_above_threshold` error class** added — distinct from
    `coverage_below_threshold` so a `review_skip_zone` refusal (skip
    refused because zone is fully readable) can be disambiguated by
    agents branching on error class.
  - **Role-enforcement on `review_finding` / `review_defer` /
    `review_commit_fix`** — these gates now reject session-tokens
    whose role doesn't match per the spec (`review-zone` for finding,
    `fix-cycle` for defer/commit). Untagged tokens (legacy / unit
    tests) still pass through.
  - **Dedup batches persist to `passes.jsonl`** as a new
    `dedup_batches` row kind. Producer crash mid-dedup rehydrates the
    SAME batch layout at startup so dedup-batch sub-claims keep their
    `file_groups` rather than seeing an empty list.
  - **Template populates `iter_num` and `assigned_findings`** in
    `fix-fan-out` / `re-review-affected` userdata. `openFixPartition`
    now emits a `findings_by_zone` map; the executor projects to the
    child's bound zone so per-zone fix agents see only their own
    assigned IDs.
  - **`agent-run` comment about runId honest about per-Execute MCP
    creation** — no cross-Execute bearer reuse; the comment now says
    so.
  - **Recovery test covers zone-plan + dedup-batches rehydration**
    paths.
  - **`runAgent` tests cover the empty-fan-out terminal** for
    fix-cycle and re-review (no stores + pass-state-only).
