# Spec: `rimsky compose run` — one-shot in-process orchestrator

This spec adds a new CLI verb that drives a compose manifest to terminal state in a single command, with no external infrastructure to stand up. The CLI binary hosts the runtime stack in-process, dispatches to executors the manifest names, captures every run as a forensic on-disk artifact, and exits with a status reflecting the aggregate outcome.

The work expands `concept:rimsky` from "thin HTTP client over the control-api" to "thin HTTP client plus embedded one-shot runtime." It does not introduce new platform mechanisms; every component the verb composes (the unified launcher, the compose engine, the persistence backends, the host-agent's binary-spawning path) already exists in rimsky-core and is reused unchanged or with surgical extension.

## User outcomes

### STORY-one-shot-to-terminal

As an operator with a compose manifest, I can drive its declared instances to terminal state with one invocation, so that I can run orchestrations on a machine without standing up rimsky infrastructure.

**Acceptance:** The operator invokes the one-shot orchestrator against a compose manifest whose declared instances target reachable executors. Every declared instance reaches terminal state (success or failure), the orchestrator stops on its own, and the operator can observe the per-instance outcomes when it returns.

**Falsifier:** The orchestrator exits before every declared instance reaches terminal; OR it stalls after the work is finished and has to be killed; OR it requires a separate teardown step before its results can be read.

**Proof:** demo + executable proof — drive a two-instance manifest where one succeeds and one fails; the run completes on its own and reports both outcomes.

### STORY-audit-artifact

As an operator, I can inspect the durable record of a completed one-shot run, so that I can debug failures and verify successful runs without re-running.

**Acceptance:** After the orchestrator exits, a durable record of the run lives at a stable, discoverable location; the operator opens it and reads the recorded instance terminations, node-run history, attribute values, and event log directly, using widely-available tooling for the format.

**Falsifier:** No durable record survives the process exit; OR the record contains only state metadata (last-known status flags) without per-node-run history; OR the record is in a format that requires rimsky-specific tooling to query.

**Proof:** demo — drive a small failing manifest, then walk through opening the artifact and pulling the failing node-run's terminal event out by hand.

### STORY-spawned-local-services

As a developer or consumer-project author, I can declare local executor binaries that get spawned for a single run, so that consumer projects can ship a binary plus a one-line wrapper instead of a service installer.

**Acceptance:** The operator initiates a one-shot run, naming a local executor binary by path. The orchestrator spawns the binary, the manifest's nodes that reference it execute through it, and when the run exits the spawned process exits with it.

**Falsifier:** The binary spawns but the manifest's nodes don't reach it (their dispatches fail or hang); OR the binary spawns but is leaked after the verb exits (visible as a stray process); OR the spawn requires the operator to also run a separate long-lived daemon first.

**Proof:** executable proof — small manifest with one node referencing a stub local executor; the run launches the binary, drives the node through it to success, exits; a post-exit process check confirms no leak.

### STORY-live-progress

As an operator watching a one-shot run, I can see per-node lifecycle as it happens, so that I have situational awareness during execution and can distinguish hangs from healthy work.

**Acceptance:** During the run, the operator observes lifecycle output emitted in time with execution — at minimum, one line per instance start, one line per instance terminal, and one line per node-run terminal, ordered chronologically as they occur.

**Falsifier:** The terminal stays silent until the run ends and then dumps everything at once; OR the lines appear but are batched well after the events they describe; OR per-node terminals show only counts ("3 nodes done") without naming which nodes or with what outcomes.

**Proof:** demo — record a transcript of a multi-instance manifest with one slow node; show the progress lines appearing as the node executes, not after it returns.

### STORY-script-friendly-outcome

As an operator integrating one-shot orchestration into a script (CI / build / wrapper), I can branch on the run's outcome class, so that the surrounding script knows whether to proceed, fail, or treat the run as bounded-out.

**Acceptance:** A wrapper script invoking the one-shot orchestrator can distinguish three outcome classes from the orchestrator's exit status: all-instances-success, at-least-one-failure, and a wall-clock bound exceeded (when the operator chose to bound the run). The script can branch on these without parsing log output.

**Falsifier:** The orchestrator returns the same exit code for all-success and at-least-one-failure (script can't branch); OR a bounded run that hits its limit returns the same exit code as a clean failure (script can't distinguish bound-killed from failed-and-completed); OR the exit code varies by manifest particulars (script can't write a stable rule).

**Proof:** executable proof — three runs (clean success / one failed instance / a bounded run hitting its limit) verified to produce three distinct exit codes via a wrapper that exits 0, 1, 2 respectively.

## Architecture

The verb composes four existing rimsky-core surfaces and adds one new wiring layer:

```
                 rimsky compose run <manifest>
                            │
                            ▼
            ┌─── 1. Artifact root discovery ───┐
            │     Walk cwd → first .rimsky/    │
            │     (or --workdir override)      │
            └──────────────────────────────────┘
                            │
                            ▼
            ┌─── 2. Role runners + orchestrator ────┐
            │     code:lib/control/launch          │
            │     • SQLite at .rimsky/runs/<id>/    │
            │       state.db (migrated by the verb  │
            │       before the runners start)       │
            │     • Filesystem blob backend at      │
            │       .rimsky/runs/<id>/blobs/        │
            │       (small under threshold stay     │
            │       inline-in-row)                  │
            │     • Control-api on 127.0.0.1:0     │
            │     • env:RIMSKY_PROCESS_ROLE=unified│
            └──────────────────────────────────────┘
                            │
                            ▼
            ┌─── 3. Service registry seeded ────┐
            │     • From manifest blocks        │
            │     • From sibling rimsky.yml     │
            │     • From --service spawns       │
            │       (host-agent path)           │
            └───────────────────────────────────┘
                            │
                            ▼
            ┌─── 4. Compose engine (reused) ────┐
            │     code:cmd/rimsky/cli/compose/  │
            │     Dials loopback control-api    │
            │     via code:cli/client.go        │
            └───────────────────────────────────┘
                            │
                            ▼
            ┌─── 5. Terminal-wait loop ─────────┐
            │     Poll instance states until    │
            │     all declared instances reach  │
            │     instance-terminal             │
            └───────────────────────────────────┘
                            │
                            ▼
            ┌─── 6. Graceful drain ─────────────┐
            │     SIGTERM children → wait 5s →  │
            │     SIGKILL → close control-api → │
            │     close .db → update            │
            │     .rimsky/latest symlink → exit │
            └───────────────────────────────────┘
```

Reused unchanged: the three role runners in `code:lib/control/launch/` — `RunScheduler`, `RunSupervisor`, `RunControlAPI` — each of which loads its YAML config from disk via the existing `env:RIMSKY_CONFIG` and `env:RIMSKY_SUPERVISOR_CONFIG` env vars; the role-orchestration pattern from `code:cmd/rimsky-entrypoint/main.go::runUnified` (start the three runners in order, track their stop functions, select on signal or role-failure, drain in reverse order); the compose engine `code:cmd/rimsky/cli/compose/` (already reconciles a manifest against a control-api endpoint via `code:cmd/rimsky/cli/client.go::Client`); the sqlite persistence at `code:lib/foundation/persistence/sqlite/`; the inline + filesystem-spill blob backends at `code:lib/foundation/persistence/blob_config.go` and `code:lib/foundation/persistence/blob_filesystem.go`; the host-agent spawn path used by `code:cmd/rimsky/cli/run.go`'s existing `--service <name>=<path>` flag.

Extended: the compose manifest schema gains an `executors:` block and a `claim_producers:` block mirroring the existing `executors:` and `claim_producers:` blocks in the rimsky.yml schema (the wrapper types in `code:lib/control/config/stores.go::LoadRimskyConfigYAML` — `yamlExecutorEntry` and `yamlClaimProducerEntry`); the `--service` flag is wired into `compose run` with the same semantics as `code:cmd/rimsky/cli/run.go::resolveServiceBindings`.

New: the verb itself (a new dispatch case under `code:cmd/rimsky/cli/compose/cmd.go::Dispatch`), the artifact-root discovery walker, a sibling orchestration site under `code:cmd/rimsky/cli/compose/` modeled on `runUnified` that writes synthetic config YAMLs to the run directory and points the role runners at them via env vars, the terminal-wait loop, the per-node lifecycle progress printer, the graceful-drain coordinator, the `.rimsky/latest` symlink updater.

## Behavior

### Startup sequence

1. **Parse flags.** The verb accepts `<manifest-path>` plus `--name <name>`, `--workdir <path>`, `--timeout <duration>`, `--quiet`, `--verbose`, `--json`, and `--service <name>=<path>` (repeatable). Flag parsing rejects unknown values; an empty manifest path is a usage error.
2. **Load and validate the manifest.** `code:cmd/rimsky/cli/compose/manifest.go::LoadManifest` runs the same parse + validate path the existing compose verbs use. The added `executors:` and `claim_producers:` blocks are validated for endpoint shape and unique names.
3. **Discover the artifact root.** Starting from `os.Getwd()`, walk parent directories looking for the first `.rimsky/` directory. Stop at filesystem root. If none found, create `./.rimsky/` in cwd. The "artifact root" is then the directory that *contains* `.rimsky/`. `--workdir <path>` overrides discovery entirely; the supplied path becomes the artifact root (so runs land under `<workdir>/.rimsky/runs/<...>/`). The verb creates the path if it doesn't already exist.
4. **Compute the run directory.** `<root>/.rimsky/runs/<timestamp>-<name>/`, where `<timestamp>` is `time.Now().UTC().Format("2006-01-02T15-04-05Z")` and `<name>` is `--name` if provided else `manifest.Project`. Both go through the project regex `^[a-z][a-z0-9-]{0,62}$` for filesystem safety. Collision (run directory already exists, e.g., from a rapid second invocation) appends `-2`, `-3`, ... until a fresh path lands.
5. **Spawn `--service` binaries.** For each `--service <name>=<path>` flag, the verb spawns the binary using the same exec + ready-poll mechanism that today's host-agent daemon uses in `code:lib/runtime/hostagent/spawn.go::handleSpawn` — pick a free port, set `env:RIMSKY_AGENT_PORT` in the child's environment, exec the binary, poll-dial `127.0.0.1:<port>` until ready (bounded by a default 30-second readiness timeout). The verb extracts the existing exec/ready-poll dance into a reusable helper that both the host-agent daemon and the compose-run verb call; the host-agent's proxy chain (`concept:host-agent-proxy`) is not used here because supervisor is in-process and dials the spawned endpoint directly. If readiness times out, the verb exits non-zero with a clear error naming the binary and the path, after reaping the partial spawn (no leak).
6. **Materialize the synthetic config YAMLs.** The role runners load config from disk via `env:RIMSKY_CONFIG` and `env:RIMSKY_SUPERVISOR_CONFIG`; there is no programmatic config seam. So the verb writes two YAML files into the run directory:
   - `<run>/rimsky.yml` — the unified config: `cfg:persistence.driver=sqlite` with `cfg:persistence.sqlite.path=<run>/state.db`; `cfg:persistence.blob.backend=filesystem` with `cfg:persistence.blob.filesystem.root=<run>/blobs/` and the default `SpillThresholdBytes` (values at or under the threshold stay inline in the SQL row; values above spill to a file under the root); the merged `executors:` and `claim_producers:` blocks (from the manifest + any sibling `rimsky.yml` + spawned `--service` endpoints from step 5, applied in priority order). Publishers and named-locks blocks pass through from a sibling `rimsky.yml` only — the compose schema doesn't carry them in this work.
   - `<run>/supervisor.yml` — the supervisor-tuning file (concurrency, heartbeat, callback host/port, advertise host). Defaults inherited verbatim from the all-in-one baked file at `file:dockerfiles/all-in-one.supervisor-config.yml`: the callback listener binds `0.0.0.0` (matching the supervisor concept's stated invariant that the listener binds on the all-interfaces address), while `advertise_host: 127.0.0.1` keeps the dialed-back endpoint loopback-only for one-shot mode. No invariant departure.
   Both files are persisted alongside `state.db` and `blobs/` as part of the run artifact — operators reading a post-mortem run see exactly what config the run used.
7. **Run migrations.** Call `driver.Migrate` directly against the (empty) sqlite database before starting any role runner. This mirrors the `code:cmd/rimsky-entrypoint/main.go::runMigrateIfOwned` pattern; running migrations from the verb (rather than via the `rimsky-migrate` binary) keeps the one-shot self-contained — no second process to fork.
8. **Start the role runners.** Set `env:RIMSKY_CONFIG=<run>/rimsky.yml`, `env:RIMSKY_SUPERVISOR_CONFIG=<run>/supervisor.yml`, and `env:RIMSKY_PROCESS_ROLE=unified` on the in-process env. Then start the three role runners in order — `launch.RunScheduler`, `launch.RunSupervisor`, `launch.RunControlAPI` — modeled on `code:cmd/rimsky-entrypoint/main.go::runUnified`: each runner returns a `StopFunc` and a `<-chan error`; the verb tracks stop functions for reverse-order drain and selects on a combined role-failure channel for early-exit on startup failure.
9. **Wait for control-api ready.** Poll `route:GET /v1/health` on the loopback endpoint until it responds, then proceed.
10. **Apply the manifest.** Build a `code:cmd/rimsky/cli/client.go::NewClient(loopback-endpoint)` and run the existing compose-apply path (`code:cmd/rimsky/cli/compose/apply.go::RunComposeUp`'s underlying engine), which registers templates, deploys them, and creates the declared instances. Each `CreateInstanceRequest` sent by `compose run` sets `code:cmd/rimsky/cli/client.go::CreateInstanceRequest.TerminateAfterRun=true` so the instance self-terminates once its nodes settle — the same mechanism `rimsky run --no-keep` already uses for its single-template equivalent.
11. **Enter the terminal-wait loop.** Poll the control-api for each declared instance's state. When every instance has reached an instance-terminal state, exit the loop and proceed to shutdown.

### Service resolution

A node in a template declares an executor by short name (`executor: foo`). The supervisor resolves the short name against the service registry, which `compose run` populates from three sources in priority order (lowest to highest):

1. **Sibling `rimsky.yml`** (if present in the same directory as the manifest). The existing config loader reads it.
2. **Compose manifest's `executors:` and `claim_producers:` blocks** (the new schema fields). Each entry carries `transport`, `endpoint`, `tls`, and protocol metadata, mirroring `code:lib/control/config/stores.go::ExecutorEntry` and `::StoreEntry`.
3. **`--service <name>=<path>` flags.** Repeatable. Each flag spawns the binary via the host-agent path and registers the resulting loopback endpoint. The bare-name form (`--service foo`) resolves via the alias file the same way `rimsky run` does today.

A name appearing in multiple sources resolves to the highest-priority source; the supervisor sees one endpoint per name.

### Progress output

The default progress printer streams per-node lifecycle to stderr:

- One line per instance creation: `instance <project>:<name>: created`.
- One line per node-run terminal: `instance <project>:<name>: <node-id> → <outcome> [<reason>]`. `<outcome>` is one of `success | failure | parked | infra`. `<reason>` appears only on non-success outcomes (the canonical signal type-path per `concept:signal`).
- One line per instance terminal: `instance <project>:<name>: <outcome> (frames: <n>)`.

Output is chronologically ordered as events arrive. Lines are flushed line-by-line (no buffering).

`--quiet` suppresses all progress lines; the verb prints only the final aggregate summary (one line per instance, plus the exit status) and exits. `--verbose` adds frame-tick lines, dispatch decisions, and claim-handle events. `--json` switches every progress line to a JSON object on a single line (one JSON object per line, no array wrapper) suitable for piping into `jq`. The final aggregate summary follows the same format.

### Termination semantics

The verb waits for every declared instance to reach an instance-terminal state per `concept:terminal-resolution`. Parked nodes are handled by the supervisor's existing policy: time-wake (snooze parks), external invalidate, or watchdog timeout (`parked → failed{error_class: "park_timeout"}` per `concept:parked-state`). The verb does not implement park-aware logic of its own; it just waits.

Exit codes:

- **0** — every declared instance reached terminal-success.
- **1** — at least one declared instance reached terminal-failure (any failure class, including `park_timeout`).
- **2** — `--timeout <duration>` elapsed before all instances reached terminal.
- **130** — SIGINT received during shutdown (conventional Unix code).

`--timeout` is opt-in with no default. When unset, the verb waits as long as the manifest takes.

### Graceful shutdown

Shutdown is triggered by SIGINT, SIGTERM, `--timeout` expiry, or natural completion (all instances terminal). On any trigger:

1. The supervisor stops accepting new dispatches.
2. In-flight dispatches are signaled to terminate gracefully (the supervisor's existing cancellation path).
3. Spawned child processes (`--service` binaries) receive SIGTERM.
4. A 5-second grace window elapses (hardcoded, no flag).
5. Any in-flight dispatch or spawned child still running receives SIGKILL.
6. The control-api HTTP server stops.
7. The sqlite connection is closed.
8. The `.rimsky/latest` symlink is replaced atomically (`symlink + rename`) to point at the just-completed run directory.
9. The verb exits with the determined exit code.

A second SIGINT during shutdown escalates to hard exit: send SIGKILL to all spawned children immediately, close the database connection without waiting, exit. The `.rimsky/latest` symlink may not be updated in this path.

### Artifact contents

After the verb exits, `<root>/.rimsky/runs/<timestamp>-<name>/` contains:

- `state.db` — the sqlite database with every rimsky table populated by the run: `table:rimsky_frames`, `table:rimsky_node_events`, the node-runs table, the instances table, the templates table, the attributes table, etc. Queryable with the standard `sqlite3` CLI or any sqlite-aware tool.
- `blobs/` — the filesystem blob spill root, populated by attribute writes exceeding `SpillThresholdBytes`. The 2-level sha256 fanout under the spill root per `code:lib/foundation/persistence/blob_filesystem.go::FilesystemBackend.derivePath`: `<hh>/<hh>/<rest-of-hash>-<unix-nano>-<seq>.bin`.
- `rimsky.yml` and `supervisor.yml` — the synthetic config files the role runners read; persisted as part of the run's record of "what config was used."

`<root>/.rimsky/latest` is a symlink updated after each run to point at the most-recent run directory.

## Technical decisions

### TD-cli-verb

**Choice:** Add `rimsky compose run <manifest>` as a new sub-verb under the existing compose dispatcher, sibling to `up | down | plan | status`. The existing four verbs' precondition (a running rimsky reachable over the control-api) is unchanged; the new verb is the one that does not require it.

**Rationale:** Sits naturally alongside the existing compose family. The verb operates on the same manifest format with the same engine; verb-naming consistency makes the surface scannable.

### TD-exposure-no-config

**Choice:** One-shot mode is exposed only via the new verb. No operator-facing knob is added to the persistence config block or any other operator-facing config surface.

**Rationale:** Simplest usage model. Operators running a deployed rimsky stack cannot accidentally select the embedded mode; the simplest deployment path stays unchanged.

### TD-persistence-driver

**Choice:** Use the existing sqlite persistence driver, pointed at `<run>/state.db`. Do not add an in-memory variant.

**Rationale:** The forensic file is the goal, not ephemeral state. The existing sqlite driver needs no setup and produces a queryable artifact in a widely-supported format.

**Alternatives considered:** A new in-memory backend (would gut the audit story; introduces a new conformance surface to maintain). An in-memory mode for the sqlite driver (still guts the audit story; the existing migration advisory-lock is keyed on a file path, and the per-connection database semantics of an in-memory sqlite handle would complicate the implementation).

### TD-blob-backend

**Choice:** Use the filesystem blob backend rooted at `<run>/blobs/`, with the default spill threshold. The filesystem backend keeps values at or under the threshold inline in the SQL row and spills values above the threshold to a file under the root.

**Rationale:** A single backend choice that delivers both properties: small values stay in the SQL row (good locality for the bulk of audit data; one file ships the run); large values get a sibling file under the run directory (no per-row size limit). The pure-inline backend forbids spill and would risk SQL per-row size limits for large payloads; the memory backend would gut the audit story; the postgres-large-object backend is not portable to sqlite.

**Alternatives considered:** Pure-inline backend (large-payload risk; explicitly errors on values that exceed the row-size limit). The memory blob backend (audit gap; tracked separately as the memory-blob-audit-gap tension).

### TD-artifact-layout

**Choice:** Per-run directory at `<root>/.rimsky/runs/<timestamp>-<name>/` containing `state.db` and `blobs/`. `<root>/.rimsky/latest` is a symlink to the most-recent run directory.

**Rationale:** A single folder per run is the natural archive-and-ship unit. The `latest` symlink covers the common "open the last one" case without timestamp-parsing.

### TD-artifact-root-discovery

**Choice:** From cwd, walk parent directories for the first `.rimsky/`. Create in cwd if none found. `--workdir <path>` overrides discovery entirely.

**Rationale:** A walk-up-to-first-marker discovery pattern lets operators run the verb from any subdirectory of a project and land on the same `.rimsky/`; new projects get one created on first run. The override exists for cases that need explicit placement (per-run, per-environment, scripted).

### TD-run-name

**Choice:** Default the run name from the compose manifest's `project` field. `--name <name>` overrides the default, passed through the same regex (`^[a-z][a-z0-9-]{0,62}$`) the project field is already validated against.

**Rationale:** The project field is required and already filesystem-safe by construction. Reusing the same regex for `--name` keeps the directory-name shape predictable.

### TD-timestamp-format

**Choice:** ISO 8601 UTC with colons replaced by hyphens: `YYYY-MM-DDTHH-MM-SSZ`.

**Rationale:** ISO 8601 is lexicographically chronological (so directory listings sort correctly). Hyphen-for-colon makes the path filesystem-safe on every platform; the format remains human-readable.

### TD-launch-integration

**Choice:** Reuse the three role runners — scheduler, supervisor, control-api — that the deployed `concept:supervisor`, scheduler, and `concept:control-api` binaries already run. Add a new orchestration site under the compose verb that runs them in the same pattern as the all-in-one entrypoint: start each runner in order, track each runner's stop function, select on a combined signal-or-role-failure channel, drain in reverse order. Set the process-role marker so the memory-blob backend gate (per `concept:blob-backend`) would permit memory if it were chosen.

**Rationale:** The three role runners are the natural reuse unit — the same code paths the deployed binaries run. The orchestration pattern (start / track / select / drain) is mechanical and small enough to mirror in a sibling site rather than entangle the compose verb's wiring with the entrypoint binary.

### TD-launch-config-injection

**Choice:** The verb writes two synthetic YAML files to the run directory and points the role runners at them via the existing config-path env vars — a unified config file matching the `concept:rimsky-yml` shape (persistence driver, blob backend, executors block, claim-producers block) and a separate supervisor-tuning file (concurrency, heartbeat, callback host/port, advertise host). The env vars are set on the in-process env before the runners start. The synthetic files persist alongside the SQL state and the blob root as part of the run artifact.

**Rationale:** The role runners load YAML from disk; there is no programmatic config seam today. Synthesizing on-disk files reuses the existing config-load path, costs a write per run at startup, and turns the config into an audit artifact for free — operators reading a post-mortem run see exactly what config the run used.

**Alternatives considered:** Adding a programmatic config-injection seam to the role-runner surface (rejected as a larger refactor than this verb's scope warrants).

### TD-migration-direct

**Choice:** The verb calls the persistence driver's migrate operation directly against the freshly-created sqlite database before starting any role runner. No separate migrate-binary subprocess.

**Rationale:** A one-shot run owns its database top-to-bottom; the existing migrate-binary subprocess exists to coordinate migrations across multi-process deployments, a coordination this verb does not need. Calling the migrate operation in-process keeps the verb self-contained — no second process to fork, no extra runtime-environment dependencies, no extra path for failures to take.

### TD-network-binding

**Choice:** Control-api HTTP server binds to `127.0.0.1:0` (loopback only, kernel-picked ephemeral port).

**Rationale:** OS-level isolation against other users on the same host; no port-conflict story to write between concurrent `compose run` invocations; no firewall rules to negotiate.

### TD-auth-anonymous-via-empty-key-ledger

**Choice:** Rely on the existing `concept:anonymous-mode` behavior for loopback admission: a fresh per-run sqlite database has zero rows in the API-key ledger, so every request is admitted as a synthetic admin identity. The verb provisions no keys.

**Rationale:** No new mechanism — the existing data-derived anonymous-mode rule does the right thing for a self-contained ephemeral run. Loopback-only binding (TD-network-binding) provides the network-layer isolation. Together, the run is reachable only from the host and serves every request as admin, which is the simplest one-shot model.

### TD-compose-engine-reuse

**Choice:** Reuse the existing compose engine that the `up`/`down`/`plan`/`status` verbs already use. The one-shot verb constructs a control-api client against the loopback endpoint and invokes the existing apply path.

**Rationale:** The compose engine is already an HTTP client. A direct in-process bypass of the HTTP boundary would double the wiring through the engine and risk behavioral divergence (input validation, idempotency, and error mapping all live on the HTTP boundary). The localhost round-trip cost is unmeasurable next to node-run latency.

### TD-termination

**Choice:** The verb waits for every declared instance to reach instance-terminal state per `concept:terminal-resolution`, then exits.

**Rationale:** Park is handled by the supervisor's existing policy (snooze-wake, await-callback, watchdog `park_timeout`), so parked nodes do not require special verb-level handling. The supervisor's existing instance-terminal promotion path is the natural completion gate.

### TD-instance-self-termination

**Choice:** Every instance the verb creates is created with self-termination enabled — the same `terminate_after_run` knob that `rimsky run --no-keep` already sets. The compose manifest's `InstanceRef` does not need to carry a per-instance flag; `compose run` hardcodes the value.

**Rationale:** Per `concept:instance`, an instance is durable by default and self-terminates only when created with self-termination on. Without this, the existing compose-apply path creates durable instances that never reach terminal — the verb's terminal-wait loop would spin until `--timeout` regardless of whether the actual work completed. Hardcoding the flag matches the user intent of `compose run` (a one-shot, not a deployment) and respects the existing instance invariant.

### TD-timeout-flag

**Choice:** `--timeout <duration>` is opt-in. No default.

**Rationale:** A default wall-clock cap kills legitimate long-running runs. Operators who want a guard add the flag; absence means "as long as it takes."

### TD-exit-codes

**Choice:** `0` for all-instances-success; `1` for at-least-one-failure (including `park_timeout`); `2` for `--timeout` exceeded; `130` for SIGINT during shutdown.

**Rationale:** Three distinguishable classes for script-friendly branching. `130` is the conventional shell-signaled-exit code for SIGINT (signal number 2 + 128).

### TD-progress-default

**Choice:** Per-node lifecycle: one line per instance creation, one per node-run terminal, one per instance terminal. Output goes to stderr, chronologically ordered, line-buffered.

**Rationale:** Per-node terminals are the granularity operators read live; deeper-frequency events (frame ticks, claim openings) become noise in the common case.

### TD-progress-flags

**Choice:** `--quiet` collapses output to the final aggregate summary; `--verbose` adds frame ticks, scheduler decisions, and claim-handle events; `--json` switches every line to a JSON object (JSON Lines format).

**Rationale:** Three operating modes cover CI logs (quiet), live debugging (verbose), and structured pipelines (json). They compose: `--quiet --json` is the structured CI shape.

### TD-service-spawn-flag

**Choice:** Add `--service <name>=<path>` (and bare `<name>` for aliases) to `rimsky compose run`, mirroring the flag shape on the existing `rimsky run` verb. The compose-run verb spawns binaries directly using the same exec-and-ready-poll mechanism the host-agent daemon uses today, registers each spawned endpoint in the synthetic unified config's executors block, and dispatches to the spawned port directly via the in-process supervisor. The host-agent's proxy chain (per `concept:host-agent-proxy`) is not used here because supervisor is in-process and dials the spawned endpoint directly.

**Rationale:** The spawning primitive (port-pick + env-var injection + ready-poll + child-process supervision) already exists for the host-agent daemon's use; extracting it into a reusable helper that both the daemon and the verb call avoids duplication. Exposing a familiar flag shape on the new verb means consumers and operators don't relearn the spawn surface.

### TD-services-source

**Choice:** Extend the compose manifest schema with an `executors:` block and a `claim_producers:` block, mirroring the corresponding blocks in the existing `concept:rimsky-yml` schema. Each entry is a name → entry map where the entry carries transport (executors only), endpoint, TLS mode, declared capabilities (claim producers), protocols list, and an optional observability endpoint. The claim-producers block inherits the existing `concept:rimsky-yml` rule that each entry carries a `write_semantics_allowed` list. The compose file is the primary source; a sibling unified-config file remains usable as a secondary source (loaded automatically when present in the manifest's directory). Publishers and named-locks blocks pass through from the sibling file only — the compose schema doesn't carry them in this work, since no spec'd story requires per-run publisher declarations.

**Rationale:** Single-file is the cleanest shape for the simplest usage model. Mirroring the existing block shape lets the loader reuse the same validators; operators familiar with one are immediately fluent in the other.

**Alternatives considered:** Sibling-only unified config (two-file friction). Inventing a unified `services:` namespace (departs from existing block shape).

### TD-graceful-shutdown

**Choice:** On SIGINT, SIGTERM, `--timeout` expiry, or natural completion: supervisor stops new dispatches → in-flight dispatches and spawned children receive SIGTERM → 5-second hardcoded grace → SIGKILL on anything still running → control-api stops → SQL connection closes → `latest` symlink updates → exit. A second SIGINT escalates to hard exit (immediate SIGKILL, best-effort close).

**Rationale:** 5 seconds is a conservative SIGTERM-then-SIGKILL grace — well-behaved executors unwind within it, misbehaving ones get hard-killed without blocking the operator. The second-SIGINT escape hatch is the conventional "I really mean it" fallback.

## Design changes

- **Concept: mutate `concepts/rimsky.md` in place.** Replace the `## What it is` section with:

  > Operator-facing CLI for rimsky: a thin HTTP+JSON client over the control-api for operating a deployed rimsky stack, plus an embedded one-shot orchestration mode that self-hosts the runtime stack to drive a manifest to terminal without standing up rimsky infrastructure. The CLI is the binary operators invoke directly; the embedded stack reuses the same role implementations as the deployed binaries, configured for a single ephemeral run rooted at a per-run artifact directory.

- **Tension: create `tensions/memory-blob-audit-gap.md`** with `status: open` and `category: durability`. Body:

  > ## What is muddy
  >
  > The memory variant of `concept:blob-backend` stores blob bodies in an in-process map; the persisted event log and node-run rows reference those bodies by handle. When the unified process exits, the in-process map vanishes — but the persisted rows survive, referencing handles that no longer resolve. For long-running unified deployments using the memory blob backend, "blobs are ephemeral after process exit" is the documented and intended semantic. The muddiness is what that means for the audit trail: an operator (or post-mortem tool) reading the persisted event log encounters blob handles that resolve to nothing, with no in-band indicator distinguishing structural absence (memory blobs never persisted) from data loss (a backend that lost its data). A reader holding only the persisted event log cannot tell which case they are looking at.
  >
  > ## Evidence
  >
  > The memory backend is implemented at `code:lib/foundation/persistence/blob_memory.go` and gated to `env:RIMSKY_PROCESS_ROLE=unified` in `code:lib/foundation/persistence/blob_config.go`. Event-log writes reference blob handles uniformly across backends; no per-backend metadata flag indicates resolution-time absence semantics.
  >
  > ## Resolution candidates
  >
  > - Annotate memory-backend handles with a flag at write time so a reader knows they will not resolve after process exit, and surface that flag in event-log responses.
  > - Restrict the memory backend further: legal only when no persisted audit consumer is configured (e.g., no lifecycle subscriber, no operator dashboard), so the gap cannot leak to a post-mortem reader.
  > - Retire the memory backend; require unified-mode deployments to use inline or filesystem blobs.
  > - Document the gap as a known characteristic of the memory backend and leave the resolution semantics unchanged.

- **Story: create `stories/one-shot-to-terminal.md`** capturing STORY-one-shot-to-terminal verbatim — role, capability, business value, Acceptance, Falsifier, Proof.

- **Story: create `stories/audit-artifact.md`** capturing STORY-audit-artifact verbatim.

- **Story: create `stories/spawned-local-services.md`** capturing STORY-spawned-local-services verbatim.

- **Story: create `stories/live-progress.md`** capturing STORY-live-progress verbatim.

- **Story: create `stories/script-friendly-outcome.md`** capturing STORY-script-friendly-outcome verbatim.

- **Decision: create `decisions/cli-verb.md`** capturing TD-cli-verb verbatim — Choice, Rationale.

- **Decision: create `decisions/exposure-no-config.md`** capturing TD-exposure-no-config verbatim.

- **Decision: create `decisions/persistence-driver.md`** capturing TD-persistence-driver verbatim — Choice, Rationale, Alternatives.

- **Decision: create `decisions/blob-backend.md`** capturing TD-blob-backend verbatim — Choice, Rationale, Alternatives.

- **Decision: create `decisions/artifact-layout.md`** capturing TD-artifact-layout verbatim.

- **Decision: create `decisions/artifact-root-discovery.md`** capturing TD-artifact-root-discovery verbatim.

- **Decision: create `decisions/run-name.md`** capturing TD-run-name verbatim.

- **Decision: create `decisions/timestamp-format.md`** capturing TD-timestamp-format verbatim.

- **Decision: create `decisions/launch-integration.md`** capturing TD-launch-integration verbatim.

- **Decision: create `decisions/launch-config-injection.md`** capturing TD-launch-config-injection verbatim — Choice, Rationale, Alternatives.

- **Decision: create `decisions/migration-direct.md`** capturing TD-migration-direct verbatim.

- **Decision: create `decisions/network-binding.md`** capturing TD-network-binding verbatim.

- **Decision: create `decisions/auth-anonymous-via-empty-key-ledger.md`** capturing TD-auth-anonymous-via-empty-key-ledger verbatim.

- **Decision: create `decisions/compose-engine-reuse.md`** capturing TD-compose-engine-reuse verbatim.

- **Decision: create `decisions/termination.md`** capturing TD-termination verbatim.

- **Decision: create `decisions/instance-self-termination.md`** capturing TD-instance-self-termination verbatim.

- **Decision: create `decisions/timeout-flag.md`** capturing TD-timeout-flag verbatim.

- **Decision: create `decisions/exit-codes.md`** capturing TD-exit-codes verbatim.

- **Decision: create `decisions/progress-default.md`** capturing TD-progress-default verbatim.

- **Decision: create `decisions/progress-flags.md`** capturing TD-progress-flags verbatim.

- **Decision: create `decisions/service-spawn-flag.md`** capturing TD-service-spawn-flag verbatim.

- **Decision: create `decisions/services-source.md`** capturing TD-services-source verbatim — Choice, Rationale, Alternatives.

- **Decision: create `decisions/graceful-shutdown.md`** capturing TD-graceful-shutdown verbatim.

The created concept/story/decision/tension bodies follow the self-containment rule (no file paths, no `code:` citations, no external-doc references). The spec's `## Architecture` and `## Behavior` sections carry the code-citation narrative; the `## Technical decisions` section above is written in self-contained form so each TD can be captured verbatim into its `decisions/<slug>.md` body. The tension body's `## Evidence` section is permitted to cite code per the tensions carve-out in the self-containment rule.

## Manifest

### Stories

- **STORY-one-shot-to-terminal** — operator drives a manifest's instances to terminal with one invocation (Proof: demo + executable proof)
- **STORY-audit-artifact** — operator inspects the durable record after a one-shot run (Proof: demo)
- **STORY-spawned-local-services** — operator declares local executor binaries that get spawned per-run (Proof: executable proof)
- **STORY-live-progress** — operator sees per-node lifecycle as it happens (Proof: demo)
- **STORY-script-friendly-outcome** — operator branches on exit code class in a wrapper script (Proof: executable proof)

### Technical decisions

- **TD-cli-verb** — new `rimsky compose run <manifest>` verb under compose dispatcher
- **TD-exposure-no-config** — one-shot mode exposed only via the verb, no operator config knob
- **TD-persistence-driver** — existing sqlite backend pointed at a file
- **TD-blob-backend** — inline + filesystem-spill, rooted at the per-run blob dir
- **TD-artifact-layout** — `<root>/.rimsky/runs/<timestamp>-<name>/` containing `state.db` + `blobs/`; `latest` symlink
- **TD-artifact-root-discovery** — walk up from cwd for first `.rimsky/`, else create in cwd; `--workdir` overrides
- **TD-run-name** — default from `Manifest.Project`; `--name` overrides through the same regex
- **TD-timestamp-format** — `YYYY-MM-DDTHH-MM-SSZ` (ISO 8601 UTC, colons hyphenated)
- **TD-launch-integration** — reuse the three role runners; new orchestration site under the compose package, modeled on the all-in-one entrypoint
- **TD-launch-config-injection** — synthetic `rimsky.yml` + `supervisor.yml` written to the run dir; env vars point the role runners at them
- **TD-migration-direct** — verb calls the persistence-driver migrate operation directly; no separate migrate-binary subprocess
- **TD-network-binding** — control-api binds to `127.0.0.1:0`
- **TD-auth-anonymous-via-empty-key-ledger** — rely on existing anonymous mode (empty API-key ledger) for admission; loopback binding provides the network isolation
- **TD-compose-engine-reuse** — reuse existing compose engine dialing the loopback endpoint
- **TD-termination** — wait for every declared instance to reach instance-terminal
- **TD-instance-self-termination** — every instance the verb creates has `terminate_after_run` enabled (the existing `--no-keep` knob applied to every instance)
- **TD-timeout-flag** — `--timeout` opt-in, no default
- **TD-exit-codes** — 0 success, 1 failure, 2 timeout, 130 SIGINT
- **TD-progress-default** — per-node lifecycle on stderr, chronologically ordered
- **TD-progress-flags** — `--quiet`, `--verbose`, `--json` flags
- **TD-service-spawn-flag** — extend `--service <name>=<path>` from `rimsky run`
- **TD-services-source** — extend compose manifest with `executors:` and `claim_producers:` blocks
- **TD-graceful-shutdown** — soft drain with hardcoded 5s grace; second SIGINT escapes

### Design-doc changes

- **`concepts/rimsky.md`** — mutate `## What it is` (CLI now hosts an embedded one-shot runtime alongside the HTTP client surface)
- **`tensions/memory-blob-audit-gap.md`** — create (open, category: durability)
- **`stories/one-shot-to-terminal.md`** — create
- **`stories/audit-artifact.md`** — create
- **`stories/spawned-local-services.md`** — create
- **`stories/live-progress.md`** — create
- **`stories/script-friendly-outcome.md`** — create
- **`decisions/cli-verb.md`** — create
- **`decisions/exposure-no-config.md`** — create
- **`decisions/persistence-driver.md`** — create
- **`decisions/blob-backend.md`** — create
- **`decisions/artifact-layout.md`** — create
- **`decisions/artifact-root-discovery.md`** — create
- **`decisions/run-name.md`** — create
- **`decisions/timestamp-format.md`** — create
- **`decisions/launch-integration.md`** — create
- **`decisions/launch-config-injection.md`** — create
- **`decisions/migration-direct.md`** — create
- **`decisions/network-binding.md`** — create
- **`decisions/auth-anonymous-via-empty-key-ledger.md`** — create
- **`decisions/compose-engine-reuse.md`** — create
- **`decisions/termination.md`** — create
- **`decisions/instance-self-termination.md`** — create
- **`decisions/timeout-flag.md`** — create
- **`decisions/exit-codes.md`** — create
- **`decisions/progress-default.md`** — create
- **`decisions/progress-flags.md`** — create
- **`decisions/service-spawn-flag.md`** — create
- **`decisions/services-source.md`** — create
- **`decisions/graceful-shutdown.md`** — create
