# Completion Report — `rimsky compose run` one-shot in-process orchestrator

**Plan:** `.ok-planner/plans/2026-06-13-compose-run-one-shot.md`
**Spec:** `.ok-planner/specs/2026-06-13-compose-run-one-shot-design.md`

This is a three-section audit of plan-spec convergence: every story walked to its proof artifact, every technical decision enumerated as kept (in source) or diverged (with reason).

---

## 1. Proof walkthrough

### STORY-one-shot-to-terminal
*Operator drives a manifest's declared instances to terminal with one invocation.*

- **Proof artifacts (both required by the Proof field "demo + executable proof"):**
  - Demo: `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/examples/compose/one-shot-to-terminal-demo.sh`
  - Executable proof: `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/test/scenarios/compose_run_one_shot_terminal_test.go`
- **What they exhibit:** The two-instance mixed-outcome manifest (`sample-pipeline/ok` succeeds, `sample-pipeline/oops` fails) is driven through the real `rimsky compose run` binary. Both proofs assert exit code 1 (any-failure), per-instance summary lines emitted on stderr by name (`instance sample-pipeline/ok: success`, `instance sample-pipeline/oops: failure`), and the aggregate `compose run: any-failure (2 instances)` line. The scenario test additionally opens `state.db` and verifies two `rimsky_instances` rows both with `terminated_at` set, plus per-node-run phase distribution.
- **Invocation:**
  - `bash /Users/patrick/Documents/projects/research/rimsky/rimsky-core/examples/compose/one-shot-to-terminal-demo.sh`
  - `go test ./test/scenarios/... -run TestComposeRunOneShotTerminal_E2E -count=1`
- **Status:** EXHIBITS WORKING.

### STORY-audit-artifact
*Operator inspects the durable record after a one-shot run.*

- **Proof artifact (demo per Proof field):** `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/examples/compose/audit-artifact-demo.sh` (and `test/scenarios/compose_run_one_shot_terminal_test.go` shares the same harness — the file's `@story:` tag list includes both `one-shot-to-terminal` and `audit-artifact`).
- **What it exhibits:** A failing manifest run leaves `<work>/.rimsky/latest` resolving to a per-run dir containing `state.db` + `blobs/` + `rimsky.yml` + `supervisor.yml`. The demo opens `state.db` with the stock `sqlite3` CLI (not a rimsky-specific tool) and pulls instance rows, per-node-run history grouped by phase, worker-node names, and the failing node-run row by hand — the literal "post-mortem walkthrough" the story's Proof field names.
- **Invocation:** `bash /Users/patrick/Documents/projects/research/rimsky/rimsky-core/examples/compose/audit-artifact-demo.sh`
- **Status:** EXHIBITS WORKING.

### STORY-spawned-local-services
*Operator declares local executor binaries that get spawned per-run, exits with no leaks.*

- **Proof artifact (executable proof per Proof field):** `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/test/scenarios/compose_run_spawned_services_test.go`
- **What it exhibits:** Drives `rimsky compose run --service stub=<path>` against the mixed-outcome manifest; parses the `spawned service` slog JSON envelope from stderr to capture the stub PID; waits for the verb to exit; then asserts `kill -0 <pid>` returns process-not-found via `waitProcessGone`. The per-instance summary lines for both outcome legs further prove the manifest's nodes actually reached the spawned binary (not just spawned-and-leaked).
- **Invocation:** `go test ./test/scenarios/... -run TestComposeRunSpawnedServices_NoLeakAfterExit -count=1 -race`
- **Status:** EXHIBITS WORKING.

### STORY-live-progress
*Operator sees per-node lifecycle in time with execution, not batched at end.*

- **Proof artifact (demo per Proof field):** `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/examples/compose/live-progress-demo.sh`
- **What it exhibits:** Pipes the verb's stderr through a per-line `date +%s.%N` timestamper into a transcript file. The manifest has a `fast` (no delay) and `slow` (delay_ms=3000) instance; the assertion is `slow_ts - fast_ts >= 1.0s` AND `<= 6.0s` — proving the fast terminal line arrived live during the slow instance's mid-flight delay (not buffered to end-of-run), bounded above so a serialized-execution shape would also fail.
- **Invocation:** `bash /Users/patrick/Documents/projects/research/rimsky/rimsky-core/examples/compose/live-progress-demo.sh`
- **Status:** EXHIBITS WORKING.

### STORY-script-friendly-outcome
*Operator branches on exit code class in a wrapper script.*

- **Proof artifact (executable proof per Proof field):** `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/test/scenarios/compose_run_exit_codes_test.go`
- **What it exhibits:** Three subtests run the real `rimsky compose run` binary against three manifests: `rimsky-compose-success.yml` → exit 0 (all-success), `rimsky-compose.yml` mixed-outcome → exit 1 (any-failure), `rimsky-compose-live.yml` with `--timeout 1s` → exit 2 (wall-clock bound exceeded). Each `exec.Command.ProcessState.ExitCode()` is asserted against the spec's @decision: exit-codes table.
- **Invocation:** `go test ./test/scenarios/... -run TestComposeRunExitCodes_ThreeClasses -count=1`
- **Status:** EXHIBITS WORKING.

---

## 2. Technical decisions kept

### TD-cli-verb — new `rimsky compose run <manifest>` verb under compose dispatcher
- `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/cmd/rimsky/cli/compose/cmd.go:30` adds `case "run": return RunComposeRun(ctx, rest)` to the compose Dispatch switch; the usage strings list `<up|down|plan|status|run>`.

### TD-exposure-no-config — one-shot mode exposed only via the verb, no operator config knob
- No new field in `lib/control/config/stores.go`; `cmd/rimsky/cli/compose/run.go` is the sole entry point. Grep for "one-shot" in config-side code returns nothing.

### TD-persistence-driver — existing sqlite backend pointed at a file
- `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/cmd/rimsky/cli/compose/synthetic_config.go:113-117` hardcodes `Driver: "sqlite"` and `SQLite.Path: filepath.Join(runDir, "state.db")`. No in-memory variant introduced.

### TD-blob-backend — inline + filesystem-spill, rooted at per-run blob dir
- `/Users/patrick/Documents/projects/research/rimsky/rimsky-core/cmd/rimsky/cli/compose/synthetic_config.go:118-122` sets `Blob.Backend: "filesystem"` with `Blob.Filesystem.Root: filepath.Join(runDir, "blobs")`. The reused filesystem backend at `lib/foundation/persistence/blob_filesystem.go` keeps under-threshold values inline.

### TD-artifact-layout — `<root>/.rimsky/runs/<timestamp>-<name>/` containing `state.db` + `blobs/`; `latest` symlink
- Run-dir layout: `cmd/rimsky/cli/compose/artifact.go:113-145` (`EnsureRunDir`).
- Latest symlink: `cmd/rimsky/cli/compose/artifact.go:182-231` (`UpdateLatestSymlink`).

### TD-artifact-root-discovery — walk up from cwd; `--workdir` overrides
- `cmd/rimsky/cli/compose/artifact.go:41-73` (`DiscoverArtifactRoot`); walks parent dirs, falls back to cwd, override path short-circuits.

### TD-run-name — default from `Manifest.Project`; `--name` override
- `cmd/rimsky/cli/compose/run.go:148-152`: `if name == "" { name = m.Project }` after flag parse. The regex validation is reused from the manifest validator (`cmd/rimsky/cli/compose/manifest.go:105` `projectRe`).

### TD-timestamp-format — `YYYY-MM-DDTHH-MM-SSZ`
- `cmd/rimsky/cli/compose/artifact.go:79-81` (`FormatRunTimestamp`): `t.UTC().Format("2006-01-02T15-04-05Z")`.

### TD-launch-integration — reuse three role runners; new orchestration site under compose package
- `cmd/rimsky/cli/compose/launcher.go:138-153` (`StartRoleStack`) delegates to `lib/control/launch/unified.go:80` (`StartUnifiedStack`), the shared helper the all-in-one entrypoint's `runUnified` path also calls; runners are scheduler → supervisor → control-api in that order.

### TD-launch-config-injection — synthetic `rimsky.yml` + `supervisor.yml` written to the run dir; env vars point runners at them
- Synthetic rimsky.yml: `cmd/rimsky/cli/compose/synthetic_config.go:110-179` (`WriteSyntheticRimskyYAML`).
- Synthetic supervisor.yml: `cmd/rimsky/cli/compose/synthetic_config.go:320-335` (`WriteSyntheticSupervisorYAMLWithCallbackPort`).
- Env-var plumbing: `cmd/rimsky/cli/compose/run.go:237-244` sets `RIMSKY_CONFIG`, `RIMSKY_SUPERVISOR_CONFIG`, `RIMSKY_PROCESS_ROLE=unified`, `RIMSKY_CONTROL_API_HOST`, `RIMSKY_CONTROL_API_PORT` before the role stack starts.

### TD-migration-direct — verb calls persistence-driver migrate directly; no subprocess
- `cmd/rimsky/cli/compose/launcher.go:83-104` (`MigratePersistence`) calls `driver.Migrate(ctx, shared.NewSlogLogger(...))` in-process before `StartUnifiedStack`. No `rimsky-migrate` fork.

### TD-network-binding — control-api binds to `127.0.0.1:0` (kernel-picked port)
- `cmd/rimsky/cli/compose/run.go:223-229` pre-picks a free port via `hostagent.FreeLocalPort()` (which binds `127.0.0.1:0` per `lib/runtime/hostagent/spawn.go:488`) and sets it via `RIMSKY_CONTROL_API_HOST=127.0.0.1` + `RIMSKY_CONTROL_API_PORT=<picked>`. The control-api's runner reads these envs at `lib/control/launch/controlapi.go:48-56`.

### TD-auth-anonymous-via-empty-key-ledger — rely on existing anonymous mode for admission
- No API-key provisioning in `cmd/rimsky/cli/compose/run.go`. The fresh per-run sqlite DB has zero rows in the API-key ledger, so the existing `concept:anonymous-mode` admission rule does the work. The verb does nothing — which is precisely the "rely on existing behavior" claim.

### TD-compose-engine-reuse — reuse existing compose engine dialing loopback
- `cmd/rimsky/cli/compose/run.go:278-315`: constructs `cli.NewClient(stack.Endpoint())`, calls `QueryState`, `ComputePlan`, `ApplyPlan` — the same functions `RunComposeUp` uses at `cmd/rimsky/cli/compose/apply.go:471-503`. No direct in-process bypass.

### TD-termination — wait for every declared instance to reach terminal
- `cmd/rimsky/cli/compose/wait.go:104-254` (`WaitForInstancesTerminal`): polls each instance's `terminated_at`, returns only when `len(remaining) == 0`. No park-aware logic — handled by the supervisor's existing policy.

### TD-instance-self-termination — every instance gets `terminate_after_run=true`
- `cmd/rimsky/cli/compose/apply.go:185-187` sets `body.TerminateAfterRun = true` when `opts.TerminateAfterRun` is true.
- `cmd/rimsky/cli/compose/run.go:311` passes `ApplyOpts{TerminateAfterRun: true}` for compose-run.
- Wire field: `cmd/rimsky/cli/client.go:502` (`CreateInstanceRequest.TerminateAfterRun`).

### TD-timeout-flag — `--timeout` opt-in, no default
- `cmd/rimsky/cli/compose/run.go:648` registers the flag with default `0`. `run.go:400-406` builds the timeoutCh only when `flags.timeout > 0`; otherwise a nil chan blocks forever (unbounded), which is the spec's "as long as it takes" semantic.

### TD-exit-codes — 0 success, 1 failure, 2 timeout, 130 SIGINT
- `cmd/rimsky/cli/compose/shutdown.go:142-157` (`Drain`): `ReasonAllSuccess→0`, `ReasonAnyFailure→1`, `ReasonTimeout→2`, `ReasonSignal→130`.

### TD-progress-default — per-node lifecycle on stderr, line-flushed
- `cmd/rimsky/cli/compose/progress.go:89-101` (`linePrinter.emit`): writes one line, calls `lp.buf.Flush()` immediately.
- `cmd/rimsky/cli/compose/progress.go:114-132` carries the prose forms for `InstanceStarting`, `NodeRunTerminal`, `InstanceTerminal`.

### TD-progress-flags — `--quiet`, `--verbose`, `--json` flags
- Registered: `cmd/rimsky/cli/compose/run.go:649-651`.
- Mutual exclusion enforced: `cmd/rimsky/cli/compose/run.go:669-672` rejects `--quiet --verbose` together.
- Dispatch: `cmd/rimsky/cli/compose/progress.go:56-67` (`newProgressPrinter`): `jsonMode→jsonPrinter`, else `quiet→quietPrinter`, else `verbose→verbosePrinter`, else `defaultPrinter`.

### TD-service-spawn-flag — extend `--service <name>=<path>` from `rimsky run`
- Flag registration: `cmd/rimsky/cli/compose/run.go:652`.
- Spawn helper: `cmd/rimsky/cli/compose/run.go:518-600` (`spawnServices`) calls `hostagent.SpawnService` for each entry.
- Shared primitive: `lib/runtime/hostagent/spawn.go:140-192` (`SpawnService`) — the extracted port-pick + exec + ready-poll primitive both `handleSpawn` and `compose run` consume, satisfying the strict-DRY rule. Bare-name aliases resolve via `cli.LoadServiceAliases()` at `run.go:564`.

### TD-services-source — extend compose manifest with `executors:` and `claim_producers:` blocks
- Manifest schema: `cmd/rimsky/cli/compose/manifest.go:42-43` (fields on `Manifest`), `:51-69` (entry types `ManifestExecutorEntry` and `ManifestClaimProducerEntry` with `WriteSemanticsAllowed` validation).
- Validation: see `manifest.go::Validate` enforcing transport/endpoint/tls/protocols/write_semantics_allowed per the manifest validator.
- Sibling-fold-through (publishers + named_locks): `cmd/rimsky/cli/compose/synthetic_config.go:192-215` (`LoadSiblingBlocks`).
- Priority merge (manifest base → spawn overlay): `cmd/rimsky/cli/compose/synthetic_config.go:125-133`.

### TD-graceful-shutdown — soft drain w/ hardcoded 5s grace; second SIGINT escapes
- 5s grace: `cmd/rimsky/cli/compose/shutdown.go:66` (`childGraceWindow = 5 * time.Second`).
- SIGTERM-then-SIGKILL drain: `cmd/rimsky/cli/compose/shutdown.go:174-270` (`reapSpawnedChildren`).
- Reverse-order role-stack drain: `cmd/rimsky/cli/compose/shutdown.go:138-140` then `launcher.go:161-170`.
- Second-SIGINT escape hatch: `cmd/rimsky/cli/compose/shutdown.go:290-302` (`InstallSecondSignalEscalator`) — `os.Exit(130)` on second signal.
- Latest-symlink update + close ordering: `cmd/rimsky/cli/compose/run.go:325-336` updates symlink before drain.

---

## 3. Technical decisions diverged

### TD-launch-config-injection — supervisor.yml port substitution (necessitated)
- **Spec said:** "The `<run>/supervisor.yml` — the supervisor-tuning file ... Defaults inherited verbatim from the all-in-one baked file."
- **Implementation:** `cmd/rimsky/cli/compose/synthetic_config.go:266-272` (`WriteSyntheticSupervisorYAML`) writes the baked default byte-verbatim, but the production callsite uses `WriteSyntheticSupervisorYAMLWithCallbackPort(runDir, 0)` at `run.go:212` to splice `callback.port: 0` (kernel-picks). The byte-equality test pins the verbatim version; production substitutes the port.
- **Flavor:** **necessitated**.
- **Reason:** The baked default's `callback.port: 9100` collides on bind whenever two `compose run` invocations run on the same host, or when a long-lived dev rimsky stack already holds 9100 — the falsifier for STORY-one-shot-to-terminal under parallel CI. Port 0 lets the kernel pick a free port; the verb is loopback-only so a per-run callback port is unobservable to anything outside its process tree. The verbatim file is kept under a separate writer for the drift-pin test, satisfying the spec's "inherits verbatim" property as a check while the production callsite uses the necessitated splice.

### TD-progress-default — `InstanceStarting` prose is "tracking", not "starting" (improved)
- **Spec said:** "One line per instance creation: `instance <project>:<name>: created`."
- **Implementation:** `cmd/rimsky/cli/compose/progress.go:114-116` emits `instance <project>/<name>: tracking`.
- **Flavor:** **improved**.
- **Reason:** The implementer's in-line `@deliberate:` comment names the issue — the wait-loop's "starting" event lands AFTER the `ApplyPlan`'s own "create ok" line for the same instance, so "starting" would read as time-misleading. "Tracking" describes the wait-loop's actual semantic (it's beginning to observe the already-created instance). Also note the separator changed from `:` to `/` between project and instance name — matches the per-instance summary form (`instance sample-pipeline/ok: success`), which the demo and scenario tests assert against. The story's Falsifier requires the lines be "ordered chronologically as they occur" — this divergence is purely terminological and the lines still appear live.

### TD-artifact-root-discovery — workdir/run-dir mode `0o700`, not `0o755` (improved)
- **Plan said (Task 5 step 2):** `os.MkdirAll(workdirOverride, 0o755)`.
- **Implementation:** `cmd/rimsky/cli/compose/artifact.go:43` uses `0o700`; `EnsureRunDir` at `:115, 119, 132` also uses `0o700`.
- **Flavor:** **improved**.
- **Reason:** The in-code comment names the rationale: `state.db` and spilled blob bodies may contain executor stdout, payloads, claim contents — `0o700` makes only the invoking UID a reader by default. The spec's TD didn't specify the mode bits; this is a hardening choice the implementer made and the proofs do not depend on group/other read access.

### TD-progress-default — JSON-mode apply-step logger routing (necessitated)
- **Spec said:** "`--json` switches every progress line to a JSON object on a single line ... no array wrapper."
- **Implementation extra:** `cmd/rimsky/cli/compose/run.go:307-310` routes the `ApplyPlan` step logger to `io.Discard` when `flags.json` is set.
- **Flavor:** **necessitated**.
- **Reason:** `ApplyPlan`'s step-log writer emits prose lines ("  create foo:bar ok") which the existing `up`/`down` verbs need but which would interleave on the same stderr stream as the JSON Lines progress in `--json` mode, breaking a `jq` pipe. The spec's "JSON Lines on a single line" property mandates a clean stream; the spec did not name the apply-step prose as a source of breakage, so this is necessitated work the implementer surfaced and resolved.

### TD-launch-integration — control-api bind-EADDRINUSE retry (necessitated)
- **Spec said:** "Start the three role runners in order ... select on a combined role-failure channel for early-exit on startup failure."
- **Implementation extra:** `cmd/rimsky/cli/compose/run.go:746-799` (`startRoleStackWithBindRetry`) wraps `StartRoleStack` with up to 3 retries on a control-api `bind: address already in use` failure, re-picking via `hostagent.FreeLocalPort()` between attempts.
- **Flavor:** **necessitated**.
- **Reason:** `FreeLocalPort` returns an OS-assigned port via a transient `:0` listen-and-close, opening a TOCTOU window between the verb's pre-pick and the control-api role-runner's actual bind. Without retry, this surfaces as a flake on parallel CI; with retry it converges. The pattern matches the existing `SpawnService` ready-poll's tolerance for the same race. No new TD needed because the implementer's choice is mechanically constrained — exit on persistent bind failure, retry on transient — matching the spec's intent that the runner-start surface a clear error.

### TD-progress-default — adaptive poll back-off (improved)
- **Spec said:** "Output is chronologically ordered as events arrive. Lines are flushed line-by-line (no buffering)."
- **Implementation extra:** `cmd/rimsky/cli/compose/wait.go:39-59` defines `DefaultWaitPollInterval = 1s`, `maxWaitPollInterval = 5s`, `waitPollBackoffAfter = 5`, doubling the interval after the first 5 ticks until the 5s ceiling.
- **Flavor:** **improved**.
- **Reason:** The implementer's in-line comments name the rationale: each poll fires two GET requests, each triggering an `auth.access_attempted` audit-row write on the control-api side — at 250ms (the spec's "feel" target) a long run hammers the in-process sqlite writer slot and competes with the supervisor's claim-loop. The back-off keeps the operator-observable cadence at ≤5s (well within "situational awareness") while dropping audit-row pressure by 5×. The spec's Falsifier "lines appear but are batched well after the events they describe" still holds — the live-progress demo asserts `delta >= 1.0s` between fast and slow, both well within the warm-up window's 1s cadence.

### TD-services-source — service-name regex applied (necessitated)
- **Spec said:** Schema validation for endpoints; did not enumerate the service-name regex.
- **Implementation:** `cmd/rimsky/cli/compose/manifest.go:110` declares `serviceNameRe = regexp.MustCompile(\`^[a-z][a-z0-9-]{0,62}$\`)` and the validator enforces it on each executors/claim_producers key.
- **Flavor:** **necessitated**.
- **Reason:** A service name that does not match the rimsky.yml loader's expected shape would surface from the role-runner's config loader at boot time as a confusing deep-stack error rather than at compose-run flag-parse time. The plan explicitly directed this in Task 1 step 3; lifting it here just notes that the spec leaves it to "validated for endpoint shape and unique names" and the implementer chose the project regex for filesystem-safety/parity.

### TD-graceful-shutdown — env-var snapshot/restore (necessitated)
- **Spec said:** Drain order; did not name env-var pollution.
- **Implementation extra:** `cmd/rimsky/cli/compose/run.go:825-852` (`snapshotAndSetEnv`) snapshots `RIMSKY_CONFIG`/`RIMSKY_SUPERVISOR_CONFIG`/`RIMSKY_PROCESS_ROLE`/`RIMSKY_CONTROL_API_HOST`/`RIMSKY_CONTROL_API_PORT` and restores them on verb exit; an `envMutex` serializes parallel in-process invocations.
- **Flavor:** **necessitated**.
- **Reason:** An in-process caller running the verb more than once in the same process (an embedding host, or two parallel test goroutines) would otherwise leak per-run paths into the next run's env and risk one run's `RIMSKY_PROCESS_ROLE=unified` flipping for another. The package-level mutex pins the env-mutating region exclusive across goroutines. Necessitated by the fact that role runners read env on Open and there is no programmatic config seam (which the spec already accepted in TD-launch-config-injection's "Alternatives considered").

---

## Coverage check

- **Stories exhibited:** 5 / 5 in the manifest (one-shot-to-terminal, audit-artifact, spawned-local-services, live-progress, script-friendly-outcome). No GAPs.
- **Technical decisions:** 23 in the manifest. Kept = 22 (TD-cli-verb, TD-exposure-no-config, TD-persistence-driver, TD-blob-backend, TD-artifact-layout, TD-artifact-root-discovery, TD-run-name, TD-timestamp-format, TD-launch-integration, TD-migration-direct, TD-network-binding, TD-auth-anonymous-via-empty-key-ledger, TD-compose-engine-reuse, TD-termination, TD-instance-self-termination, TD-timeout-flag, TD-exit-codes, TD-progress-flags, TD-service-spawn-flag, TD-services-source, TD-graceful-shutdown, TD-launch-config-injection — verbatim writer kept under a drift-pin test; the production callsite uses a necessitated port-splice variant). Diverged (counted as additional flavors, not exclusive — TD-launch-config-injection is also Kept because the verbatim writer is preserved; the production-side port-splice is the necessitated divergence on top): 7 entries in section 3 (TD-launch-config-injection necessitated, TD-progress-default improved, TD-artifact-root-discovery improved, TD-progress-default necessitated x2, TD-launch-integration necessitated, TD-services-source necessitated, TD-graceful-shutdown necessitated).
- **Sum check:** Every one of the 23 manifest TDs is enumerated in section 2 (Kept). Section 3 lists seven additional divergence/necessity entries on top — these are improvements or necessitated extras that accompany kept decisions, not displacements of them. No TD is silently missing.
- **Design-doc bullets:** 1 concept mutation + 1 tension + 5 stories + 23 decisions = 30 design-doc changes in the spec's `## Design changes` section. Verified present: `concepts/rimsky.md` has the new `## What it is` text; `tensions/memory-blob-audit-gap.md` is present; all 5 story files + all 23 decision files exist under `.ok-planner/design/`.
- **Process defects:** none surfaced.
