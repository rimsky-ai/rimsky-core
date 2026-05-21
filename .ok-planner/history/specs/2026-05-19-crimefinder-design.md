# Crimefinder

Code-review-as-a-rimsky-graph: a separate tool that consumes rimsky's
orchestration primitives to run zone-partitioned, design-doc-aware,
auto-fixing review passes over an arbitrary git repository. Findings
live in the repo as durable git-tracked JSONL; orchestration state
lives in rimsky.

## Motivation

The skill-driven approach (`/review-holistic`) does not scale past one
reviewer subagent's context window. Crimefinder lifts the structural
pattern from the prototype at `dir:../../../crimefinder/` — partition →
fan-out → per-zone subagent → persisted findings → coverage as data —
and adds two things the prototype lacks:

1. **Design-doc awareness.** Findings can cite concept slugs and tension
   slugs; the producer enforces a server-side rule that class-1-4
   findings overlapping a documented `Boundaries:` or `Invariants:`
   section without quoting it auto-route to class-5b (the "the design
   doc itself might be wrong" bucket, which only `/refine-design` +
   `/execute-plan` can resolve). The discipline lives in infrastructure,
   not in prompts.

2. **Rimsky orchestration.** Fan-out, claim lifecycle, retries, crash
   recovery, scheduling, sensor triggers all come for free from rimsky's
   existing primitives. Crimefinder ships only a custom executor, a
   custom claim-producer, template YAML, and a thin CLI wrapper. Nothing
   in rimsky's concept catalog is mutated.

The tool lives under `dir:apps/crimefinder/` in the rimsky repo for
ergonomic reasons during initial development. It is designed for
extraction to its own repository: it consumes rimsky only via public
protocol contracts (`concept:executor`, `concept:claim-producer`,
control-api), never reaches into rimsky source. After this spec's work
ships and a `/discover-design` pass bootstraps crimefinder's own
`.ok-planner/`, extraction is mechanical.

## Scope of this spec

Read-and-fix review pass against a single repository: full producer
surface, full executor gate vocabulary including the fix-cycle's
atomic commit-fix gate and test-running gate, the rimsky template that
drives a pass end-to-end (review fan-out + dedup + bounded fix-cycle
sub-graph), JSONL data formats, configuration shapes, testing strategy,
and observability surface.

Explicit non-goals (deferred to follow-up specs):

- Sensor configurations for cron / webhook / concept-doc-edit-watch
  triggers.
- `route:GET /observability/review-queue` cascade-graph dashboard
  route for surfacing the class-5 queue (bare path per
  `tension:control-api-version-prefix`).
- Multi-repo coordination (one-rimsky-stack-per-repo is the v1 promise).
- Metrics / SLO instrumentation beyond structured logs.
- Crimefinder's own design-doc bootstrap (handled later via
  `/discover-design` against the shipped code).
- A sensor that closes class-5b items automatically when a
  `/refine-design` reconciliation spec lands.

## Architecture

### Deployment topology

The rimsky stack and the crimefinder producer run in Docker; the
crimefinder executor runs as a host process so it can spawn Claude CLI
against the user's host filesystem with host auth credentials.
Containers and the host executor agree on file paths via bind mounts at
identical absolute paths.

| Component | Where | Reason |
|---|---|---|
| `rimsky-supervisor`, `rimsky-control-api`, `postgres` | Docker | Unchanged from rimsky baseline |
| `crimefinder-producer` | Docker, bind-mounts host repo at identical absolute path | Container-side git/JSONL ops; serializes writes via in-process mutex |
| `crimefinder-executor` | Host process, binds `127.0.0.1:7071` gRPC | Must spawn Claude CLI as host subprocess with host `env:ANTHROPIC_API_KEY` / `env:CLAUDE_CODE_OAUTH_TOKEN` |
| `crimefinder` CLI wrapper | Host process | Thin wrapper over rimsky control-api |
| Claude CLI subprocess | Spawned by host executor; `cwd` set to a host path on the bind-mounted tree | Where Claude actually runs |

Networking:

- **Supervisor → executor** (`Execute()` + async-callback POSTs): from
  container to host via `host.docker.internal:7071` on macOS / Docker
  Desktop, or `--add-host=host.docker.internal:host-gateway` on Linux.
- **Executor → producer** (gate calls): host to container via
  `localhost:<mapped-port>`; producer's gRPC port is port-mapped in
  the compose file.
- **Supervisor async-callback advertise host**: the supervisor must
  advertise a host-reachable address back to the host executor. Set
  `cfg:callback.advertise_host` (or `env:RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST`)
  to e.g. `host.docker.internal:8090`. This is the documented mitigation
  for `tension:callback-hostname-split`.
- **CLI → control-api**: standard host-to-container port mapping
  (`localhost:<control-api-mapped-port>`).

The atomic commit-fix gate (the cross-cutting case worth getting right)
is enclosed entirely inside the producer process: when fired, the
producer holds a commit-mutex, performs `git add` + `git commit` against
the bind-mounted tree, then appends the JSONL status-update row, then
releases. No host/container boundary crossed inside the transaction.

### Service inventory

- **`crimefinder-executor`** — TypeScript, host process, implements
  `concept:executor` over gRPC. Spawns Claude CLI per dispatch with
  `--allowedTools Read,Glob,Grep,Edit,Write,mcp__crimefinder__review_*`
  (no `Bash`, no other shell). Hosts an internal MCP server (loopback
  HTTP-JSON-RPC, same pattern as `dir:executors/claude-agent/`) that
  exposes the `review_*` gate vocabulary. Each gate delegates the
  side-effecting work to the producer over gRPC; the gate is a thin
  validation + routing layer.

- **`crimefinder-producer`** — TypeScript, containerized, implements
  `concept:claim-producer` for lifecycle (Open/Commit/Abandon/Release/
  Capabilities/SplitScope/ScopesConflict) AND a tool-specific typed-data
  gRPC service (`CrimefinderState`) for the actual operations. Same
  process, two protocols on one gRPC server. Per `concept:data-processing`'s
  documented split, claim-producer is control-plane; the typed service
  is the substrate-direct data plane. Owns: zone partitioning (lifted
  from prototype), JSONL append/query, git ops, test invocation, fix
  atomicity.

- **`crimefinder` CLI wrapper** — TypeScript, host binary built with
  `npm run build`. Thin shell over rimsky control-api: `pass`, `status`,
  `up`, `down`. No business logic.

- **Template YAML(s)** — Static artifacts under
  `dir:apps/crimefinder/templates/` registered with rimsky's control-api
  at deploy time via `cmd:rimsky template register`. One template:
  `code-review-pass`.

### Subfolder layout

```
apps/crimefinder/
  executor/        # TS host-process executor
  producer/        # TS containerized producer
  cli/             # TS host CLI wrapper
  shared/          # Shared types (gate I/O, JSONL rows, error classes)
  templates/       # Template YAML
  deploy/          # Compose file additions (producer container)
  test/
    scenarios/     # Scenario tests with stub-mode executor
    e2e/           # Real-Claude-CLI smoke (manual, gated)
  cold-read/       # Pointers to rimsky's; diverges only if needed
  feature-index.md
  CHANGELOG.md
  CLAUDE.md
  README.md
```

**Template DSL drift note for the planner.** The current rimsky design
docs (`.ok-planner/design/concepts/fan-out.md`,
`concept:claim-co-holdership`, `concept:scope`) use `claims:` as the
node-side directive for acquiring claims. The current rimsky template
parser and shipped template fixtures
(`file:quickstart/example-template.yml`, `file:test/smoke/fixtures/template.yml`,
`code:foundation/spec/template.go::TemplateNodeDef.Stores`) still use
`stores:`. This is a documented doc-vs-code drift in rimsky. The spec
body uses `claims:` per the design-doc vocabulary; the planner writing
YAML fixtures should emit `stores:` until rimsky's parser is updated.
Both refer to the same template directive.

The component is internally consistent: nothing under `apps/crimefinder/`
imports from elsewhere in the rimsky repo at the source level. Producer
and executor share types via `apps/crimefinder/shared/` (gate signatures,
JSONL row shapes, error-class taxonomy). After extraction, the
`apps/crimefinder/` content moves to a new repo root unchanged.

`apps/` is a new top-level directory in the rimsky repo, intentionally
parallel to (not contained in) `executors/` and `stores/`. The choice
is informed by `concept:module-layout`'s four-module split — `apps/`
is outside that split because crimefinder is a consumer of rimsky's
modules, not a member of any of them. Rimsky's existing depguard
boundaries do not need entries for `apps/`.

## Producer surface

### One combined producer

The producer owns the repo as a whole — both source tree and findings
artifacts. Splitting into separate repo-tree and findings producers
would buy clean separation of concerns at the cost of `concept:claim-co-holdership`
coordination for what is, in practice, one transactional unit (`git
add` + JSONL append + `git commit`). Easier to start combined and split
later if reuse demands it.

### Two protocols, one process

The producer's gRPC server exposes:

1. **`concept:claim-producer`** for lifecycle. The mandatory 4-verb
   protocol (Open / Commit / Abandon / Release) plus the
   `Capabilities()` startup handshake, plus the optional `SplitScope`
   and `ScopesConflict` mix-ins that crimefinder advertises in its
   capabilities. Rimsky uses this to grant and reap claims.

2. **`CrimefinderState`** typed-data gRPC service for the actual
   operations the executor's gates invoke. Operations:
   `AppendFinding`, `QueryFindings`, `UpdateFindingStatus`,
   `AppendCoverage`, `RunTests`, `CommitFix`, `DeferFinding`,
   `SkipZone`, `RequestHelp`, `AggregateFindings`. Each validates
   against the producer's authoritative state (JSONL files + working
   tree) before mutating.

The producer process hosts a second gRPC service, `CrimefinderState`,
that the executor's gates call for typed-data operations. Rimsky never
sees this service; it is internal to crimefinder. The claim-producer
protocol grants access (via `Open`'s returned address, which carries
the typed service's endpoint URL + a one-time session token); the
typed service is what holders actually use for read/write operations.

### Scope kinds and address shapes

Two scope kinds, each producing a distinct `ClaimResult.address`:

**Source-tree zone scope** (subgraph lifetime, one per fan-out child).
Produced by `SplitScope` on a parent source-tree claim:

```json
{
  "kind": "source-tree-zone",
  "pass_id": "p_<id>",
  "zone_id": "z_<id>",
  "zone_label": "src/feature_a",
  "zone_files": ["src/feature_a/foo.ts", "src/feature_a/bar.ts"],
  "repo_root_path": "/host/path/to/repo",
  "state_endpoint_url": "localhost:7081",
  "session_token": "<one-time bearer; producer-generated>"
}
```

**Pass-state scope** (subgraph lifetime, held by `open-pass` and co-held
by every dispatching node; auto-released via `concept:auto-terminal` at
end of pass):

```json
{
  "kind": "pass-state",
  "pass_id": "p_<id>",
  "state_endpoint_url": "localhost:7081",
  "session_token": "<one-time bearer>"
}
```

Per `concept:inertness`, these address bytes are inert in rimsky
— rimsky persists and routes them without inspection. (Byte-opaque
inertness applies here, distinct from the structural-inertness rule
that governs named-event payloads — see "Cascade events" below.) Both
the executor and the producer are crimefinder code and know the format.

### `partition_request` payload format

The `partition_request` field on each `fan_out:` block is opaque bytes
passed verbatim to the producer's `SplitScope` (per
`concept:fan-out`'s invariant: "the `partition_request` field is
opaque bytes passed verbatim to `SplitScope`; rimsky does not parse
it"). Crimefinder defines a JSON shape and routes by the fan-out
node's identity:

```jsonc
// For review-fan-out: partition over the entire source tree.
{
  "kind": "source-tree-partition",
  "pass_id": "p_<id>",
  "ignore_patterns_from_config": true
}

// For dedup-fan-out: partition over file-groups from
// aggregate-initial's claim payload.
{
  "kind": "dedup-partition",
  "pass_id": "p_<id>",
  "file_groups": <substituted from aggregate-initial payload>
}

// For fix-fan-out (inside fix-iteration sub-graph): partition over
// zones with unresolved class-1-4 findings, per iter-guard.
{
  "kind": "fix-partition",
  "pass_id": "p_<id>",
  "iter_num": <substituted from {{nodes.iter-guard.attribute.iter_num}}>,
  "affected_zones": <substituted from iter-guard.attribute>
}

// For re-review-affected.
{
  "kind": "re-review-partition",
  "pass_id": "p_<id>",
  "iter_num": <substituted from {{nodes.iter-guard.attribute.iter_num}}>,
  "affected_zones": <substituted from iter-guard.attribute>
}
```

Substitution into these payloads uses rimsky's standard `{{...}}`
grammar; the producer receives the rendered JSON bytes at SplitScope
time. The `kind` discriminator lets the producer route to the right
partitioning logic.

For `fix-partition` and `re-review-partition`, the `affected_zones`
field is sourced from `{{nodes.iter-guard.attribute.affected_zones}}`
which is a list. Rimsky's substitution renders the list as a
JSON-stringified array embedded in the partition_request bytes; the
producer parses the embedded array out of the JSON payload at
SplitScope time. Producer-side parsing is straightforward
(`JSON.parse(partition_request).affected_zones`); no special list-to-string
encoding rule is needed beyond standard JSON.

### Producer endpoint discovery

The executor does NOT receive the producer's gRPC endpoint URL via
out-of-band config; it reads the `state_endpoint_url` field from the
`ClaimResult.address` bytes for each claim it holds (per `Scope kinds
and address shapes` above). The address is supervisor-provided in
`ExecuteRequest.stores` for declared claims and `holds:` co-holders
alike. Same wire shape as `claude-agent`'s existing pattern, just
with crimefinder-defined payload bytes.

### Zone partitioning via SplitScope

`SplitScope` implements the partitioning algorithm lifted from
prototype `code:src/features/zones/partition.ts::partitionIntoZones`:
walk file tree from `repo_root`, apply ignore patterns (defaults +
config-declared additions), group files by containing directory, split
groups larger than `cfg:partitioning.max_files_per_zone`, merge small
sibling groups under `cfg:partitioning.small_group_threshold`. Returns
one sub-scope descriptor per zone; rimsky's fan-out machinery dispatches
one child per sub-scope.

`ScopesConflict` permits non-overlapping sub-path claims to coexist. Two
zone sub-claims with disjoint file lists do not conflict; rimsky can
dispatch them concurrently. Two claims on overlapping paths conflict;
rimsky serializes them.

Zones are **logical** partitions: agents have `Read`/`Glob`/`Grep`
access across the whole repo (cross-zone refs need this). The write
surface is gated through `review_commit_fix`, which validates that the
working-tree diff matches the file scope of the cited finding. An agent
editing out of zone is caught at the commit gate, not at file-write
time.

### Atomic commit-fix flow

Commit-then-append-then-recovery-scan, all inside one producer
transaction:

```
review_commit_fix gate fires (executor → producer typed-service call).
  1. Acquire commit-mutex (producer-internal).
  2. Validate prerequisites:
     - Finding exists in findings.jsonl with status:open|fixing.
     - Working tree has uncommitted changes overlapping the finding's
       file scope.
     - If cfg:require_tests_before_commit: most recent RunTests result
       in this pass returned exit:0 AND ran_at > tree_mtime_at_run.
  3. git add <changed paths matching finding's file scope>.
     Reject "no relevant changes" if the diff doesn't overlap.
  4. git commit -m "<commit_message>\n\nResolves: <finding_id>".
     On failure: release mutex; return commit_failed; no JSONL change.
     On success: capture SHA.
  5. Append findings.jsonl row:
     {kind:"status_update", ref:<finding_id>, status:"fixed",
      by_pass, resolved_at_commit:<sha>, ts:<now>}.
     Done synchronously inside the mutex.
  6. Release mutex.
  7. Emit named-event finding_resolved back to rimsky via the executor
     callback.
  8. Return {commit_sha, finding_status:"fixed"} to executor.
```

Recovery scan on producer startup: walk `git log` since the last known
JSONL-recorded resolution; for any commit with a `Resolves: <finding_id>`
footer lacking a corresponding `status:fixed` row in findings.jsonl,
append the missing row. The JSONL becomes a derived index; git history
is the source of truth for resolutions.

### Concurrency model

- JSONL appends (findings, coverage, status updates): single in-process
  mutex per file. Append throughput is low (LLM-paced); mutex doesn't
  bottleneck.
- `RunTests`: serialize across all concurrent gate calls within a pass.
  Cache result keyed on `tree_mtime_at_run`; reuse if working tree
  hasn't advanced since the cached run.
- `CommitFix`: serial via the commit-mutex described above.
- `AppendFinding`, `AppendCoverage`: parallel-safe; JSONL mutex
  serializes the actual writes but validation is lock-free.
- Parallel zone executors call producer concurrently; producer
  arbitrates per-operation.

## Executor and gate vocabulary

### Session roles

The executor is dispatched in one of two roles per the rimsky template:

- **Review-zone session**: assigned a zone, surveys it, emits findings,
  does not commit. Gates: `review_context`, `review_finding`,
  `review_coverage`, `review_complete`, optionally `review_request_help`.
- **Fix-cycle session**: assigned one or more findings in a zone, edits
  files to fix them, commits via the gate. Gates: `review_context`
  (different return shape), `review_run_tests`, `review_commit_fix`,
  `review_defer`, `review_request_help`, `review_complete`.

The role is determined by the `mission` field carried in the
dispatched node's userdata (set by the template, e.g.
`userdata: { mission: "review-zone" }` on the `review-fan-out` node).
Fan-out children re-use the parent's `rimsky_nodes` row per
`code:runtime/fanout_dispatch.go::FanOutChildRunPlan.NodeID`, so the
parent's per-node userdata reaches every child verbatim. The
crimefinder executor reads `userdata.mission` from its
`ExecuteRequest` and routes the session accordingly. `review_context`
returns the appropriate payload shape for the role.

### Tool whitelist

Claude CLI is spawned with
`--allowedTools Read,Glob,Grep,Edit,Write,mcp__crimefinder__review_*`.
No `Bash`, no `Task`, no other tool. Working-tree edits via `Edit`/`Write`;
every side effect beyond the working tree must traverse a gate.

### The gates

| Gate | Inputs | Server validations | Side effects | Returns |
|---|---|---|---|---|
| `review_context` | (derived from session token) | session live | none | `ContextPayload` (role-polymorphic; see below) |
| `review_finding` | `class:1\|2\|3\|4\|"5a"\|"5b"`, `file`, `line_start?`, `line_end?`, `symbol?`, `description`, `concept_slug?`, `tension_slug?`, `confidence:"high"\|"low"` | class-5b auto-routing rule; session in review-zone role; file path is well-formed | append `kind:"finding"` row; emit `finding_emitted` named-event | `{finding_id, effective_class, auto_rerouted}` |
| `review_coverage` | `files_read: string[]` | session in review-zone role; files exist under repo root | append coverage rows | `{recorded_count}` |
| `review_run_tests` | (none — command from config) | session not in dedup role; one test run at a time per pass | shell out to `cfg:tests.command` with `cfg:tests.timeout_seconds`; cache result keyed by `tree_mtime_at_run` | `{exit_code, output_excerpt, ran_at, cached:bool}` |
| `review_commit_fix` | `finding_id`, `fix_description`, `commit_message` | finding open, working tree dirty, tests-recent-pass if policy requires | commit-then-append flow above; emit `finding_resolved` named-event | `{commit_sha, finding_status:"fixed"}` |
| `review_defer` | `finding_id`, `reason: string` | finding is `status:open\|fixing` | append `kind:"status_update", status:"deferred"` row; emit `finding_deferred` | `{finding_id, finding_status:"deferred"}` |
| `review_skip_zone` | `reason: string` | session in review-zone role; coverage actually below threshold | record skip in pass row; emit `zone_skipped` | `{zone_id, skipped:true}` |
| `review_request_help` | `question`, `blocker_finding_id?` | (none) | append `kind:"help_request"` row; emit `help_requested` | `{help_id}` |
| `review_complete` | (none) | no findings still `status:fixing` for this session; coverage at threshold OR `review_skip_zone` invoked | mark session terminal; emit `zone_completed` | `{findings_recorded, coverage_pct}` |

### `ContextPayload` (review-zone role)

```json
{
  "pass_id": "p_<id>",
  "zone_id": "z_<id>",
  "zone_label": "src/feature_a",
  "mission": "convergence pass" | "incremental review" | "<custom>",
  "zone_files": ["src/feature_a/foo.ts", ...],
  "concept_docs": [
    {"slug": "...", "path": "...", "content": "<full markdown>"}
  ],
  "open_tensions": [
    {"slug": "...", "path": "...", "content": "<full markdown>"}
  ],
  "existing_findings_in_zone": [
    {"id": "f_...", "file": "...", "class": ..., "status": "...",
     "description_summary": "..."}
  ],
  "finding_categories_help": "<inline 5-class scheme summary>",
  "ignore_patterns": ["vendor/", ...]
}
```

### `ContextPayload` (fix-cycle role)

```json
{
  "pass_id": "p_<id>",
  "zone_id": "z_<id>",
  "zone_label": "...",
  "assigned_findings": [
    {"id": "f_...", "file": "...", "line_start": 42, "line_end": 47,
     "description": "...", "concept_slug": "...", "tension_slug": null,
     "prior_fix_attempts": []}
  ],
  "test_command": "go test ./...",
  "require_tests_before_commit": true,
  "concept_docs": [...],
  "open_tensions": [...]
}
```

Design rationale: concept docs are pushed into the payload rather than
read by the agent via `Read`. Two reasons. First, it makes "did the
agent consult the concept" a server-side knowable fact — the docs were
in front of it. Second, the producer can scope what's surfaced so an
irrelevant 200KB concept doc doesn't burn context. The agent CAN read
concept files via `Read` if it wants to verify; load-bearing material
is pushed.

### Class-5b auto-routing rule (in `review_finding`)

When a finding arrives with `class:1|2|3|4` and `concept_slug` set:

1. Producer reads `concepts/<slug>.md`.
2. Extracts the `Boundaries:` and `Invariants:` sections by heading.
3. Tokenizes each section's text (split on whitespace, lowercase,
   strip Markdown emphasis, drop tokens shorter than 4 chars).
4. Checks whether the `description` field contains a contiguous
   substring of ≥ 8 consecutive tokens from either section (verbatim
   word sequence match, case-insensitive).
5. If no match → rewrite the row's `class` to `"5b"` and set
   `auto_rerouted:true` before insertion. The returned `effective_class`
   reflects the rewrite so the agent sees what happened.

Substring matching is intentionally crude. The goal is to force the
agent to actually quote the concept's text — not to semantically judge
whether the finding is right. A semantic judge would itself be an LLM,
which is what we are explicitly avoiding by encoding the discipline
mechanically.

When a finding arrives with `tension_slug` matching an open tension
file under `cfg:design_docs.tensions_dir`: write as
`kind:"tension_confirmation"` instead of `kind:"finding"`. Counts toward
that tension's "how often does this surface" metric without polluting
the findings stream.

### Error class taxonomy

Two distinct error surfaces, with overlapping but distinct
vocabularies:

**Gate-level errors** (returned in MCP error envelopes to Claude CLI;
visible to the agent for retry / branching):

- `finding_not_found`, `finding_already_resolved`
- `working_tree_clean`, `working_tree_changes_out_of_scope`
- `tests_not_recent`, `tests_failed`, `test_command_not_configured`
- `coverage_below_threshold`
- `unresolved_findings_in_flight`
- `concept_citation_missing` (returned ALONGSIDE successful
  `review_finding` with `effective_class:"5b"` — the agent's finding
  was accepted, just rerouted)
- `commit_failed` (git itself rejected the commit — pre-commit hook,
  signing failure, etc.)
- `tension_already_cataloged` (returned alongside successful
  `review_finding` recorded as tension-confirmation)

Each carries a stable string code (above), human-readable `message`,
and `retryable: bool` hint.

**Executor-level error classes** (emitted as `Error{error_class: ...}`
on the executor protocol terminal; consumed by template `error_types:`
keys on the executor-running nodes):

- `silence_timeout` — inherited from the claude-agent pattern; emitted
  by the executor's silence detector (`env:CRIMEFINDER_EXECUTOR_SILENCE_MS`).
- `tool_error` — the agent invoked a gate that returned an
  unrecoverable error (per `retryable: false`), or the agent emitted
  a malformed MCP request. Crimefinder-defined.
- `commit_failed` — git itself rejected the fix-commit; the agent
  cannot proceed. Distinct executor-level class so the template's
  fix-zone error-policy can route this differently from `tool_error`
  (typically to `pass` with a `help_requested` named-event so the
  operator surfaces it).
- `tests_failed` — `review_run_tests` returned non-zero; the agent
  has no fix path; iteration `pass`es so the next iter-guard can
  re-evaluate.

Error-class strings are not globally registered. Per
`code:graph/node/template_validator.go`, any string is acceptable as
an `error_types[<class>]` key on a node; the template's `error_types:`
declarations define the accepted vocabulary for that node's executor
emissions. Crimefinder's template declares the four classes above on
each `executor: crimefinder` node it ships.

Gate-level errors are agent-visible; executor-level error classes are
template-visible. The two domains overlap (e.g., `commit_failed` and
`tests_failed` exist in both) — the executor decides whether a gate
error escalates to an executor-terminal error based on policy in the
gate's return: if the gate returns `retryable: false` AND the agent
cannot continue without that gate's success (judged by the gate's
own escalation flag in its response), the executor terminates with
the corresponding executor-level class.

### Cascade events

Each side-effecting gate emits one `concept:named-event` back to rimsky
over the executor protocol. These persist to the named-event ledger
(`table:rimsky_node_events`, NOT the supervisor's `rimsky_events`
audit log) and are available downstream via `{{nodes.<emitter>.event.<name>.<path>}}`
substitution and via `subscribes: [{node: <emitter>, on: event,
name: <event_name>, ...}]` declarations per `concept:node-subscription`.
(The legacy `on_event:` handler map is retired per
`tension:_resolved/send-vs-subscribe-asymmetry`; templates that reference
the retired form are rejected at registration.)

Event names: `pass_opened`, `pass_closed`, `zone_started`,
`zone_completed`, `zone_skipped`, `finding_emitted`, `finding_resolved`,
`finding_deferred`, `finding_dedup_marked`, `tests_ran`,
`commit_failed`, `help_requested`. Per `concept:executor`, names
that templates reference (via `subscribes:` or substitution) MUST
appear in the executor's `ObservabilityCapabilities.declared_events`
at the startup handshake; rimsky validates this at template
registration when the executor is reachable via the observability
handshake. Runtime emissions of names not in `declared_events` are
persisted to `rimsky_node_events` but have no consumer (per
`concept:named-event`'s "unknown-name no-op" rule). Crimefinder's
executor declares all twelve names at startup.

Payload shape (constant wrapper, event-specific `data`):

```json
{
  "event": "finding_resolved",
  "pass_id": "p_<id>",
  "zone_id": "z_<id>",
  "session_id": "<rimsky node-run id>",
  "ts": "<ISO8601>",
  "data": {"finding_id": "f_...", "commit_sha": "...", "iter_num": 1}
}
```

Per `concept:inertness`, named-event payloads are **structurally**
inert (rimsky may JSON-walk for substitution but does not interpret
domain meaning) — distinct from the byte-opaque inertness rule that
governs address bytes.

## Template and graph topology

One template, `code-review-pass`, with two graphs: `main` and a
`fix-iteration` sub-graph invoked via `delegate:` per
`concept:sub-graph` / `concept:delegation`.

### Template params

- `repo_root` (path; required)
- `mission` (string; default `"convergence pass"`)
- `fix_cycle_cap` (int; default `3`) — number of pre-declared
  `fix-iteration` delegate sites in `main`

### `main` graph node sequence

Notes on rimsky template DSL used below:

- Fan-out per `concept:fan-out` is declared on the parent node via
  `fan_out:`. The parent acquires its claim; the producer's
  `SplitScope` returns N sub-scope descriptors; rimsky opens N
  sub-claim handles atomically (per `concept:fan-out`'s sub-claim
  atomicity invariant per `@blessed-invariant 10`); rimsky dispatches
  N child leaf runs of the same node. There is no separate "fan-out
  consumer" node.
- Node ordering follows the post-2026-05-14 subscription-cascade
  pattern: cascade coupling is declared receiver-side via
  `subscribes:`, and the substitution-ref parser auto-subscribes on
  `{{nodes.<X>.attribute.<Y>}}` references. So nodes that already
  carry an attribute reference upstream get their subscription for
  free; nodes that depend on upstream completion without a value
  dependency need an explicit `subscribes: [{node: <X>, on: state}]`.
  The retired `dependencies:` field is not used.
- Iteration number (`iter_num`) is **NOT carried via template
  substitution**. The producer tracks per-pass iteration count
  internally (it knows it is being asked for the Nth iter-guard /
  iter-aggregate scope for a given `pass_id`) and surfaces iter_num
  in claim payloads. The template just statically declares three
  `fix-iter-N` nodes; their relative ordering establishes the
  iteration order.

```
open-pass            claims: pass-state: { name: crimefinder,
                       selector: "@pass-state:new",
                       intent: rw, lifetime: subgraph }.
                     Producer's Open creates a new passes.jsonl
                     "pass_started" row and returns pass_id in the
                     claim payload, hoisted to attribute via
                     {{claim.pass-state.payload.pass_id}}.

discover-context     holds: pass-state: { from: open-pass };
                     claims: context-scan: { name: crimefinder,
                       selector: "@context-scan:pass_id={{nodes.open-pass.attribute.pass_id}}",
                       intent: r }.
                     Producer scans CLAUDE.md, .claude/rules/, design
                     concepts/tensions, and @concept: annotations;
                     returns the manifest as payload. Hoisted to
                     attribute context_manifest.

review-fan-out       executor: crimefinder;
                     holds: pass-state: { from: open-pass };
                     claims: source-tree: { name: crimefinder,
                       selector: "@source-tree:pass_id={{nodes.open-pass.attribute.pass_id}}",
                       intent: r };
                     fan_out: { claim: source-tree,
                       partition_request: <see "partition_request
                       payload format" below>,
                       error_policy: { kind: best_effort } };
                     userdata: { mission: "review-zone" }.
                     Prompt template references
                     {{nodes.discover-context.attribute.context_manifest}}
                     so the parser auto-subscribes to discover-context.
                     Rimsky dispatches one child leaf run per zone
                     sub-claim returned by SplitScope.

aggregate-initial    holds: pass-state: { from: open-pass };
                     claims: agg: { name: crimefinder,
                       selector: "@aggregate-findings:pass_id={{nodes.open-pass.attribute.pass_id}}",
                       intent: r };
                     subscribes: [{ node: review-fan-out, on: state }].
                     Producer's Open reads findings emitted by
                     review-fan-out's leaf runs and returns
                     class_1_4_remaining count, class_5 list, and
                     dedup_file_groups in payload, hoisted to
                     attributes via {{claim.agg.payload.*}}.

dedup-fan-out        executor: crimefinder;
                     holds: pass-state: { from: open-pass };
                     claims: dedup-grouping: { name: crimefinder,
                       selector: "@dedup-grouping:pass_id={{nodes.open-pass.attribute.pass_id}}",
                       intent: rw };
                     fan_out: { claim: dedup-grouping,
                       partition_request: <see "partition_request payload format">,
                       error_policy: { kind: best_effort } };
                     userdata: { mission: "dedup" }.
                     Prompt template references
                     {{nodes.aggregate-initial.attribute.dedup_file_groups}}
                     so the parser auto-subscribes.

class-split          holds: pass-state: { from: open-pass };
                     claims: split: { name: crimefinder,
                       selector: "@class-split:pass_id={{nodes.open-pass.attribute.pass_id}}",
                       intent: r };
                     subscribes: [{ node: dedup-fan-out, on: state }].
                     Producer reads post-dedup state and returns
                     class_1_4_remaining (bool) and class_5_findings
                     (list) in payload.

fix-iter-1           delegate: fix-iteration;
                     holds: pass-state: { from: open-pass };
                     subscribes: [{ node: class-split, on: state }].
                     (The pass-state holds is declared on the calling
                     node in main — per concept:delegation it merges
                     into the absorbed entry's row, giving the
                     sub-graph a local handle on pass-state without
                     violating concept:sub-graph's internal-references-
                     local-only invariant.)

fix-iter-2           delegate: fix-iteration;
                     holds: pass-state: { from: open-pass };
                     subscribes: [{ node: fix-iter-1, on: state }].

fix-iter-3           delegate: fix-iteration;
                     holds: pass-state: { from: open-pass };
                     subscribes: [{ node: fix-iter-2, on: state }].
                     (Count = fix_cycle_cap; pre-declared. Iterations
                     where no class-1-4 work remains short-circuit
                     inside the sub-graph via iter-guard.)

class-5-finalize     holds: pass-state: { from: open-pass };
                     claims: c5: { name: crimefinder,
                       selector: "@class-5-finalize:pass_id={{nodes.open-pass.attribute.pass_id}}",
                       intent: r };
                     subscribes: [{ node: fix-iter-3, on: state }].
                     Producer ensures every class-5 row has status:open
                     and returns the final class-5 queue as payload.

report               holds: pass-state: { from: open-pass };
                     claims: rpt: { name: crimefinder,
                       selector: "@report:pass_id={{nodes.open-pass.attribute.pass_id}}",
                       intent: rw };
                     subscribes: [{ node: class-5-finalize, on: state }].
                     Producer writes pass_finished row to
                     passes.jsonl and returns summary as payload.
                     When report terminates, pass-state's holding
                     subgraph has no remaining active co-holders, so
                     concept:auto-terminal fires Commit on the
                     pass-state claim_handle (per
                     concept:claim-lifetime: subgraph-lifetime claims
                     are promoted to committed at subgraph
                     completion).
```

### `fix-iteration` sub-graph

Sub-graph encapsulation note: per `concept:sub-graph`, internal
nodes can only reference other internal nodes within the same
sub-graph or the entry alias. Internal nodes therefore reference
`iter-guard` (the entry alias) for both `holds:` and substitution
into selectors. The entry alias resolves per-invocation to the
calling node (one of `fix-iter-N` in `main`), where `open-pass`
IS visible — so `iter-guard`'s own attribute `pass_id` can source
from `{{nodes.open-pass.attribute.pass_id}}` correctly, and
downstream sub-graph nodes pick it up via
`{{nodes.iter-guard.attribute.pass_id}}`.

```yaml
graphs:
  - name: fix-iteration
    entry: iter-guard
    exit: iter-aggregate
    nodes:
      - type: iter-guard
        # The calling fix-iter-N node carries holds: pass-state from
        # open-pass; per concept:delegation that merges into this
        # entry's row, so the entry has the pass-state claim. No
        # explicit holds: declaration here.
        claims:
          unresolved-check:
            name: crimefinder
            selector: "@unresolved-class-1-4:pass_id={{nodes.open-pass.attribute.pass_id}}"
            intent: r
        attributes:
          schema:
            properties:
              pass_id:
                # Sourced from open-pass — legal here because, after
                # absorption, this node sits in main alongside open-pass.
                type: string
                source: "{{nodes.open-pass.attribute.pass_id}}"
              iter_num:
                type: integer
                source: "{{claim.unresolved-check.payload.iter_num}}"
              affected_zones:
                type: array
                source: "{{claim.unresolved-check.payload.affected_zones}}"
              skipped:
                type: boolean
                source: "{{claim.unresolved-check.payload.skipped}}"
        # The producer tracks per-pass iteration count internally,
        # returns iter_num (1, 2, or 3) in the payload of the Nth
        # call to @unresolved-class-1-4 for this pass_id. If the
        # producer finds no unresolved class-1-4 findings, it returns
        # skipped:true and an empty affected_zones list.

      - type: fix-fan-out
        executor: crimefinder
        holds:
          pass-state: { from: iter-guard }
        claims:
          fix-partition:
            name: crimefinder
            selector: "@fix-partition:pass_id={{nodes.iter-guard.attribute.pass_id}}&iter_num={{nodes.iter-guard.attribute.iter_num}}"
            intent: rw
        fan_out:
          claim: fix-partition
          # Producer's SplitScope partitions over the zones
          # iter-guard already identified as having unresolved
          # findings. If iter-guard.attribute.skipped == true,
          # SplitScope returns zero sub-scopes. With zero sub-scopes,
          # rimsky's runner falls through and dispatches the parent
          # node as a regular leaf with no sub-claim (per
          # code:runtime/runner.go's "fan-out branch fires only when
          # len(SubClaims) > 0" guard). The crimefinder executor
          # detects the absence of claim.fix-partition's sub-claim
          # address in ExecuteRequest and terminates immediately with
          # success — the "fix" mission is a no-op when there are
          # zero zones to fix. Same handling on re-review-affected.
          partition_request: <see "partition_request payload format">
          error_policy: { kind: best_effort }
        userdata:
          mission: "fix"

      - type: re-review-affected
        executor: crimefinder
        holds:
          pass-state: { from: iter-guard }
        claims:
          re-review-partition:
            name: crimefinder
            selector: "@re-review-partition:pass_id={{nodes.iter-guard.attribute.pass_id}}&iter_num={{nodes.iter-guard.attribute.iter_num}}"
            intent: r
        fan_out:
          claim: re-review-partition
          partition_request: <see "partition_request payload format">
          error_policy: { kind: best_effort }
        userdata:
          mission: "re-review"
        subscribes:
          - { node: fix-fan-out, on: state }

      - type: iter-aggregate
        holds:
          pass-state: { from: iter-guard }
        claims:
          iter-result:
            name: crimefinder
            selector: "@iter-aggregate:pass_id={{nodes.iter-guard.attribute.pass_id}}&iter_num={{nodes.iter-guard.attribute.iter_num}}"
            intent: r
        attributes:
          schema:
            properties:
              more_work_needed:
                type: boolean
                source: "{{claim.iter-result.payload.more_work_needed}}"
              findings_resolved_this_iter:
                type: integer
                source: "{{claim.iter-result.payload.findings_resolved_this_iter}}"
        subscribes:
          - { node: re-review-affected, on: state }
```

Sub-graph entry-node absorption follows `concept:delegation`: the
calling node (one of `fix-iter-1`/`fix-iter-2`/`fix-iter-3` in `main`)
absorbs the entry node `iter-guard` — same row, same executor
context. The calling node's `holds: pass-state: { from: open-pass }`
merges into the absorbed entry, so pass-state co-holdership is
established in `main` (where `open-pass` is visible), satisfying
`concept:sub-graph`'s internal-references-local-only invariant. No
userdata-substitution is needed because `iter_num` flows through
the producer's claim payload, not through template substitution
(rimsky's substitution grammar has no `{{userdata.*}}` or
`{{instance.*}}` kinds per `concept:userdata`). Downstream nodes
in the sub-graph read `iter_num` and `pass_id` via
`{{nodes.iter-guard.attribute.*}}`.

### Claim flow

- `pass-state` claim: opened by `open-pass` with `subgraph` lifetime.
  Co-held via `holds: pass-state: {from: open-pass}` by every dispatching
  node downstream of `open-pass`. Auto-released at end of pass via
  `concept:auto-terminal` after every co-holder's run is non-active.
- `source-tree` parent claim: held by `review-fan-out`. The same node
  declares `fan_out`; rimsky's parent-acquisition transaction opens
  the parent claim plus N zone sub-claims atomically. Each child leaf
  run holds one zone sub-claim with subgraph lifetime, accessible via
  the per-child `claim.source-tree` address.
- `dedup-grouping`, `fix-partition`, `re-review-partition` parent
  claims: same pattern as source-tree — declared on the fanning-out
  node, partitioned via SplitScope, sub-claims acquired atomically
  with parent.
- `ScopesConflict` semantics: per-zone sub-claims with disjoint file
  lists coexist; concurrent same-zone claims serialize.
- Computed scopes (`@aggregate-findings:...`, `@class-split:...`,
  `@iter-aggregate:...`, etc.): claimed by deterministic nodes. The
  producer's `Open` computes the requested view and returns the result
  as the claim's payload bytes; substitution into attributes via
  `{{claim.<alias>.payload.<field>}}`.

### Error policy by node kind

```yaml
# (illustrative; per error_types semantics in rimsky's template DSL)

review-zone, re-review-zone:
  error_types:
    silence_timeout: { policy: [{ action: retry, count: 1 },
                                 { action: pass }] }
    tool_error:      { policy: [{ action: retry, count: 2 },
                                 { action: pass }] }
    # default: give_up

dedup-batch:
  error_types:
    silence_timeout: { policy: [{ action: pass }] }
    tool_error:      { policy: [{ action: retry, count: 1 },
                                 { action: pass }] }

fix-zone:
  error_types:
    silence_timeout: { policy: [{ action: retry, count: 1 },
                                 { action: pass }] }
    tests_failed:    { policy: [{ action: pass }] }
    commit_failed:   { policy: [{ action: pass }] }  # surfaces via help_requested
    # default: give_up

# all deterministic nodes: no error_types → give_up on any error,
# fail the pass
```

The `pass` action on `silence_timeout` for `review-zone` and `fix-zone`
is a soft default: skip the zone with what coverage was accumulated,
continue the pass. Partial coverage is better than no pass; the report
records the skip.

## Data formats

### `.crimefinder/findings.jsonl`

Append-only, one row per line, line-delimited JSON. Four row kinds
discriminated by `kind`:

```jsonc
// kind: "finding" — the initial emit
{
  "kind": "finding",
  "id": "f_<24-char base32>",                // ~120 bits random
  "ts": "<RFC3339 with TZ>",
  "pass_id": "p_<24-char base32>",
  "zone_id": "z_<12-char base32>",           // sha256(zone_label) prefix
  "session_id": "<rimsky node-run id>",
  "class": 1 | 2 | 3 | 4 | "5a" | "5b",
  "effective_class": <same domain>,           // reflects auto-routing
  "auto_rerouted": false,                     // true when class-5b rule fired
  "file": "src/foo.ts",                       // relative to repo root
  "line_start": 42,                            // may be null
  "line_end": 47,                              // may be null
  "symbol": "handleX",                         // optional
  "description": "<free text>",
  "fingerprint": "sha256:<hex>",              // see below
  "concept_slug": "claim-handle",             // null when n/a
  "tension_slug": null,
  "confidence": "high" | "low",
  "status": "open",
  "originating_zone_id": null                 // set when cross-zone
}

// kind: "status_update" — mutation
{
  "kind": "status_update",
  "id": "<unique>",
  "ts": "<RFC3339>",
  "ref": "f_<finding id this updates>",
  "status": "fixing" | "fixed" | "deferred" | "duplicate-of" | "void"
            | "queued-to-spec" | "resolved-via-spec",
  "by_pass": "p_<id>",
  "by_session": "<rimsky node-run id>",
  "resolved_at_commit": "<git sha or null>",
  "duplicate_of": "f_<id or null>",
  "reason": "<free text; required for deferred / void>",
  "note": "<free text; optional>"
}

// kind: "tension_confirmation"
{
  "kind": "tension_confirmation",
  "id": "<unique>",
  "ts": "<RFC3339>",
  "pass_id": "p_<id>",
  "zone_id": "z_<id>",
  "tension_slug": "callback-hostname-split",
  "file": "<path>",
  "description": "<free text>"
}

// kind: "help_request"
{
  "kind": "help_request",
  "id": "<unique>",
  "ts": "<RFC3339>",
  "pass_id": "p_<id>",
  "session_id": "<rimsky node-run id>",
  "question": "<free text>",
  "blocker_finding_id": "f_<id or null>",
  "status": "open"          // status_update rows mutate by ref
}
```

Status materialization (current status of finding F1): scan rows where
`id == "F1"` OR `ref == "F1"`; sort by `ts` ascending; take the last
`status` field set. Last-write-wins by timestamp.

### Fingerprint normalization

For finding `fingerprint`:

```
normalize_description(d):
  lowercase
  strip Markdown emphasis (`*`, `_`, backticks)
  regex-replace contiguous digit runs with "<num>"
  regex-replace hex addresses (0x[0-9a-f]+) with "<hex>"
  regex-replace UUIDs ([0-9a-f-]{36}) with "<uuid>"
  collapse whitespace runs to a single space
  strip leading/trailing whitespace

fingerprint = "sha256:" + hex(sha256(
  file + "|" + (symbol or "") + "|" + normalize_description(description)
))
```

Line numbers are excluded from fingerprint inputs so fingerprints survive
code drift. Symbol is included verbatim (it's an identifier, case
matters).

### `.crimefinder/coverage.jsonl`

```jsonc
{
  "ts": "<RFC3339>",
  "pass_id": "p_<id>",
  "session_id": "<rimsky node-run id>",
  "zone_id": "z_<id>",
  "file": "<path relative to repo root>"
}
```

One row per file actually read. Aggregation per-zone collapses on
`(zone_id, file)`. Coverage % for a zone is
`|distinct_files_read ∩ zone_files| / |zone_files|`.

### `.crimefinder/passes.jsonl`

Two row kinds; same `id` on both:

```jsonc
// kind: "pass_started" — written by open-pass
{
  "kind": "pass_started",
  "id": "p_<id>",
  "ts": "<RFC3339>",
  "mission": "<string>",
  "trigger": "manual" | "cron" | "webhook" | "concept_edit_watch",
  "trigger_metadata": { },                  // free-form
  "template_hash": "sha256-<hex>",
  "fix_cycle_cap": 3,
  "params_hash": "sha256-<hex>"
}

// kind: "pass_finished" — written by report
{
  "kind": "pass_finished",
  "ref": "p_<id of pass>",
  "ts": "<RFC3339>",
  "exit_reason": "complete" | "interrupted" | "failed" | "partial",
  "zones_planned": 12,
  "zones_completed": 11,
  "zones_skipped": 1,
  "findings_emitted": 47,
  "findings_resolved": 31,
  "findings_deferred": 5,
  "findings_class_5_remaining_open": 11,
  "fix_cycle_iterations_run": 2,
  "coverage_pct": 96.7,
  "commits": ["abc123...", "def456..."]
}
```

A pass with `pass_started` and no `pass_finished` is in flight or
crashed. Liveness distinguishable via `rimsky_instances` / `rimsky_node_runs`
state.

### `.crimefinder/config.yml` (consumer-repo, committed)

```yaml
tests:
  command: "go test ./..."
  timeout_seconds: 600
  cwd: "."                              # relative to repo root

require_tests_before_commit: true

coverage:
  threshold_pct: 80
  on_below_threshold: "require_skip"   # require_skip | warn | allow

partitioning:
  max_files_per_zone: 50
  small_group_threshold: 10
  additional_ignore_patterns:
    - "vendor/"
    - "third_party/"

allowed_tools:                          # rarely overridden
  - "Read"
  - "Glob"
  - "Grep"
  - "Edit"
  - "Write"
  - "mcp__crimefinder__review_*"

design_docs:                            # optional
  concepts_dir: ".ok-planner/design/concepts"
  tensions_dir: ".ok-planner/design/tensions"
  annotation_marker: "@concept:"
```

If `design_docs` is absent, crimefinder still runs but with no
class-5b auto-routing (every finding stays at its emitted class).

### Consumer-repo `rimsky.yml` additions

Consumer adds to their existing `rimsky.yml`:

```yaml
executors:
  crimefinder:
    transport: grpc
    endpoint: "host.docker.internal:7071"
    tls: off
    protocols: [executor]

claim_producers:
  crimefinder:
    endpoint: "grpc://crimefinder-producer:9100"
    protocols: [claim_producer]
    write_semantics_allowed: [sync]
```

Crimefinder does not ship its own top-level config; integration is via
the consumer's existing `rimsky.yml`. Auth tokens loaded via env vars,
not inline.

### Gate error envelope (MCP-side, returned to Claude CLI)

```jsonc
{
  "code": -32000,
  "message": "Finding f_xyz123 already resolved (status: fixed)",
  "data": {
    "crimefinder_error_class": "finding_already_resolved",
    "retryable": false,
    "finding_id": "f_xyz123",
    "current_status": "fixed",
    "resolved_at_commit": "abc123..."
  }
}
```

`crimefinder_error_class` matches the taxonomy above. `retryable`
advises the agent whether the same call could succeed on retry.

### ID schemes

| Prefix | Length | Source |
|---|---|---|
| `p_` | 24 chars base32 | random at `open-pass` |
| `f_` | 24 chars base32 | random at `review_finding` |
| `z_` | 12 chars base32 | first 12 base32 chars of sha256(zone_label); deterministic — lifted from prototype `code:src/features/zones/partition.ts::generateZoneId` |
| Coverage / status-update / help-request row IDs | any unique scheme (ULID or random base32) | uniqueness only |

## Testing strategy

### Unit tests (Vitest, `*_test.ts` co-located)

Per the cold-read convention. Coverage targets:

- **Producer**:
  - `AppendFinding` validation (each error class)
  - `CommitFix` flow with mocked git binary (success, dirty-required, no-overlap, commit-failure)
  - Fingerprint normalization (idempotency, drift cases, special chars)
  - Partition algorithm (lifted with prototype tests as fixtures)
  - `SplitScope` / `ScopesConflict` predicates (overlap, non-overlap)
  - Class-5b auto-routing substring matcher (positive, negative, edge cases on tokenization)
  - Tension-confirmation routing
  - Mutex serialization under simulated contention
  - Recovery scan reconstruction from `git log`

- **Executor**:
  - MCP shim routing (each gate hits the right typed-API method)
  - `--allowedTools` construction (review-zone vs fix-cycle role)
  - Prompt template rendering for both roles
  - Error envelope shape per error class
  - Named-event payload construction for each event name

- **Shared (`apps/crimefinder/shared/`)**:
  - JSONL row schema validators (round-trip parse/serialize)
  - Gate input validators (table-driven)

### Scenario tests (`dir:apps/crimefinder/test/scenarios/`)

Per rimsky's `dir:test/scenarios/` pattern. Spin up real producer
container + stub-mode executor; verify end-to-end behavior under the
supervisor. Require Docker for testcontainers.

- **Full pass against a fixture repo, stub executor.** Verifies graph
  topology: `open-pass` → `discover-context` → `review-fan-out` →
  `aggregate-initial` → `dedup-fan-out` → `class-split` → `fix-iter-1`
  → `class-5-finalize` → `report`. Asserts: JSONL rows match expected
  shapes; `pass-state` claim is committed at terminal; supervisor
  records no orphans.

- **Multi-zone fan-out with concurrent stub executors.** Verifies JSONL
  append serialization under contention; coverage rows aggregate per
  zone correctly.

- **Fix-cycle iteration shape.** Stub findings + stub fixes. Verifies
  `iter_num` parameter wiring, `more_work_needed` aggregation, the
  early-termination behavior when iter-2 has no work
  (`iter-guard` short-circuits, sub-graph passes through).

- **Crash recovery.** Kill producer process between `git commit` and
  JSONL append; restart; verify recovery scan reconstructs the missing
  `status:fixed` row from the commit's `Resolves:` footer.

- **Cross-zone finding.** Stub agent emits a finding referencing an
  out-of-zone file; verify it's attributed to the zone containing the
  file with `originating_zone_id` set to the dispatching zone.

- **Re-discovery dedup.** Run the same pass twice against the same repo
  with no intervening changes; verify second pass produces zero new
  findings (all dedup against existing fingerprints).

- **Tension confirmation routing.** Stub agent emits a finding with a
  `tension_slug` matching an open tension file; verify the row is
  written as `kind:"tension_confirmation"` not `kind:"finding"`.

- **Class-5b auto-routing.** Stub agent emits a class-1 finding with a
  `concept_slug` whose Boundaries text the description doesn't quote;
  verify the row is written with `class:"5b"`, `auto_rerouted:true`.

- **Coverage threshold enforcement.** Stub executor calls `review_complete`
  without `review_skip_zone` and with coverage below threshold; verify
  the gate returns `coverage_below_threshold` and the session remains
  open.

### Conformance

- `cmd:rimsky-executor-conformance --endpoint host.docker.internal:7071 --transport grpc`
  against the built crimefinder executor binary. Must pass cleanly.

### End-to-end with real Claude CLI

Under `dir:apps/crimefinder/test/e2e/`. Real `claude` binary, real
Anthropic API. Gated and manual; costs API credits. Run before
significant releases.

- Single-pass smoke against a tiny fixture repo with one or two
  intentional findings. Verifies the full Claude→MCP→Gate→Producer→git
  flow at least once per major change.

### Stub mode

The executor MUST support a stub mode (per `dir:executors/claude-agent/`
pattern). `env:CRIMEFINDER_EXECUTOR_STUB_MODE=1` short-circuits the
Claude CLI spawn and returns canned outcomes driven by `userdata`
fixtures. All scenario tests run under stub mode.

## Observability

### Structured logs

- Both executor and producer log JSON lines via `pino` (or equivalent).
- Every log line stamps at least `{component, pass_id?, zone_id?, session_id?}`.
- Gate invocations log `{gate, finding_id?, outcome, latency_ms, error_class?}`.
- No PII / secret content in log payloads (tokens redacted at boundary).

### Health endpoints

- **Producer**: HTTP `GET /health` returns 200 if it can:
  - Bind to its configured gRPC port.
  - Write to `${REPO_ROOT}/.crimefinder/` (mkdir + touch).
  - Run `git status` in `${REPO_ROOT}` without error.
- **Executor**: inherits from rimsky executor pattern; existing
  `concept:observability` hook in the claude-agent-style transport.

### Named-events as audit surface

Already specified above. The named-event ledger
(`table:rimsky_node_events`) is the durable record of side-effecting
gate calls; substitution and `subscribes: [{on: event, ...}]`
declarations consume it. The supervisor's `concept:event-log`
(`rimsky_events`) separately records control-plane transitions
(acquire, dispatch, terminal, etc.) and is served at
`route:GET /events` (bare path; the control-api does not version-prefix
core routes per `tension:control-api-version-prefix`). Both surfaces
are queryable; the distinction is which kind of fact you want.

### CLI status command

`cmd:crimefinder status [--repo <path>]` (host process):

- Queries control-api for active `code-review-pass` instances on this
  repo.
- Reads recent `.crimefinder/passes.jsonl` rows; prints a table.
- Reads `.crimefinder/findings.jsonl`; aggregates open-by-class.
- Prints class-5 queue summary.

UX convenience over rimsky's control-api; no business logic.

### Metrics

Out of scope for this spec. Pass-level histograms (zones,
findings-per-class, fix-iterations) are inexpensive to add later if
demand emerges.

## Project posture

### Cold-read conventions

Crimefinder follows the rimsky cold-read style (see
`file:.claude/rules/cold-read-cheatsheet.md`): one feature per file,
tests co-located, max ~500-line files / ~100-line functions, max 3
nesting levels via early returns, no behavior-modifying decorators,
explicit parameters over DI.

Directory nesting under `apps/crimefinder/` is counted from the project
root after extraction. `apps/crimefinder/producer/<feature>/` is two
levels from the would-be project root, within the cold-read limit.

### Lineage from the prototype

Code lifted from `dir:../../../crimefinder/` (the original prototype)
is annotated with `@source:`. Specific transfers:

| Prototype file (path within prototype) | Crimefinder location | Annotation |
|---|---|---|
| `src/features/zones/partition.ts::partitionIntoZones` | `apps/crimefinder/producer/zones/partition.ts` | `@source:` |
| `src/features/zones/coverage.ts::computeZoneCoverage` | `apps/crimefinder/producer/zones/coverage.ts` | `@source:` |
| `src/features/dedup/group.ts::groupIssuesByFile` | `apps/crimefinder/producer/dedup/group.ts` | `@source:` |
| `src/features/dedup/resolve.ts::applyDedupResults` | `apps/crimefinder/producer/dedup/resolve.ts` | `@source:` |
| `src/features/issues/jsonl.ts` | `apps/crimefinder/producer/findings/jsonl.ts` | `@source:` + `@diverged: true`, `@reason: schema redesign (kind/ref discriminators, concept/tension slugs, fingerprint excludes line numbers)` |
| `src/features/mcp/tools.ts` (crime_context / crime_report / crime_complete) | split across `apps/crimefinder/executor/gates/*.ts` | `@source:` + `@diverged: true`, `@reason: typed-API rewrite, class-5b auto-routing, atomic commit-fix gate, error envelope` |

Tests where they're straight algorithmic ports also carry `@source:`.

### Crimefinder's own files (at `apps/crimefinder/`)

- `CHANGELOG.md` — separate from rimsky's, scoped to crimefinder.
- `CLAUDE.md` — crimefinder-specific agent guidance.
- `README.md` — public-facing overview.
- `cold-read/` — initially pointer file referencing rimsky's; diverges only if conventions need to.
- `feature-index.md` — crimefinder's feature/dependency map.
- `.claude/rules/` (after extraction) — mirrors rimsky's discipline.

### Crimefinder's own `.ok-planner/` not bootstrapped in this spec

The work in this spec lands code. After the code stabilizes, a separate
`/discover-design` pass over `dir:apps/crimefinder/` produces
crimefinder's own concept catalog + tensions. That bootstrap is its own
spec and is explicitly out of scope here.

## Out of scope (deferred to follow-up specs)

- Sensor configurations for trigger surfaces (cron, webhook, concept-doc-edit-watch).
- `route:GET /observability/review-queue` cascade-graph dashboard route.
- Multi-repo coordination beyond one-rimsky-stack-per-repo.
- Metrics / SLO instrumentation.
- Crimefinder's own `.ok-planner/` bootstrap.
- Auto-close of class-5b items when `/refine-design` reconciliation specs land.
- Operator UI for class-5a triage.
