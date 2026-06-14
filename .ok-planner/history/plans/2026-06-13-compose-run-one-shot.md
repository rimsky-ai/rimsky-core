# `rimsky compose run` — one-shot in-process orchestrator — Implementation Plan

**Spec:** `.ok-planner/specs/2026-06-13-compose-run-one-shot-design.md`
**Goal:** Add a `rimsky compose run <manifest>` verb that drives a compose manifest to terminal in-process, with no external infrastructure, leaving a per-run forensic artifact under `.rimsky/runs/`.
**Architecture:** New CLI sub-verb under the existing compose dispatcher. The verb discovers a `.rimsky/` root via walk-up from cwd, computes a per-run directory, optionally spawns `--service` binaries via an extracted host-agent spawn primitive, writes a synthetic `rimsky.yml`+`supervisor.yml` to the run directory, runs sqlite migrations in-process, starts the three role runners (scheduler/supervisor/control-api) modeled on `cmd/rimsky-entrypoint/main.go::runUnified`, dials the loopback control-api via the existing compose-apply engine (extended to set `TerminateAfterRun=true` on every CreateInstance), polls instance state to terminal, prints per-node lifecycle progress, and drains gracefully on signal/timeout/completion.
**Tech Stack:** Go 1.22+ stdlib + existing rimsky-core dependencies (`go-chi/chi`, `log/slog`, `modernc.org/sqlite` via the existing sqlite persistence driver, `jackc/pgx/v5`). Plumbline conventions per `.claude/rules/plumbline-cheatsheet.md` (structured-tag comments, slug-form `@blessed-invariant:` paired with tests, strict DRY for the spawn-primitive extraction).

---

## Pass 1: Compose-manifest schema extension + verb dispatch + flag parsing

**Goal:** Land the `compose run` verb's static surface: manifest schema fields, dispatcher routing, flag parser. No runtime wiring yet — this pass just plumbs the entry-point.
**Scope:** Tasks 1–4
**Falsifier:** the compose dispatcher does not route `run` (running `rimsky compose run --help` falls through to "unknown subcommand"); OR the manifest loader rejects a valid `executors:` / `claim_producers:` block; OR the parsed flags don't carry through to the run-skeleton stub.

### Task 1: Extend `Manifest` with `executors:` and `claim_producers:` blocks

**Files:** `cmd/rimsky/cli/compose/manifest.go` (mutate), `cmd/rimsky/cli/compose/manifest_test.go` (mutate)

**Spec reference:** TD-services-source.

**Background.** Today `Manifest` carries `Project`, `Context`, `Templates`, `Instances`. We add two optional map fields mirroring the existing `rimsky.yml` schema in `lib/control/config/stores.go` (`yamlExecutorEntry`, `yamlClaimProducerEntry`).

**Steps:**

1. Add two new fields to the `Manifest` struct:
   ```go
   Executors      map[string]ManifestExecutorEntry      `yaml:"executors,omitempty"`
   ClaimProducers map[string]ManifestClaimProducerEntry `yaml:"claim_producers,omitempty"`
   ```
2. Add the new entry types in the same file:
   ```go
   // ManifestExecutorEntry is one entry in the manifest's `executors:` block,
   // mirroring `lib/control/config/stores.go::yamlExecutorEntry`.
   type ManifestExecutorEntry struct {
       Transport             string   `yaml:"transport"`               // "grpc" | "http"
       Endpoint              string   `yaml:"endpoint"`                // host:port
       TLS                   string   `yaml:"tls,omitempty"`           // "" | "off" | "required"
       ObservabilityEndpoint string   `yaml:"observability_endpoint,omitempty"`
   }

   // ManifestClaimProducerEntry is one entry in the manifest's `claim_producers:`
   // block, mirroring `lib/control/config/stores.go::yamlClaimProducerEntry`.
   type ManifestClaimProducerEntry struct {
       Endpoint              string   `yaml:"endpoint"`
       Protocols             []string `yaml:"protocols,omitempty"`
       TLS                   string   `yaml:"tls,omitempty"`
       ObservabilityEndpoint string   `yaml:"observability_endpoint,omitempty"`
       WriteSemanticsAllowed []string `yaml:"write_semantics_allowed"` // required per concept:rimsky-yml
   }
   ```
3. Extend `Manifest.Validate()` with two validation blocks:
   - Each `Executors` entry: `Transport` is one of `{"grpc", "http"}`; `Endpoint` non-empty; `TLS` (if set) is one of `{"off", "required"}`. Service-name key matches `^[a-z][a-z0-9-]{0,62}$` (use a new local regex; can mirror `contextRe`).
   - Each `ClaimProducers` entry: `Endpoint` non-empty; `WriteSemanticsAllowed` non-empty (the concept:rimsky-yml invariant); each value in `WriteSemanticsAllowed` is one of `{"sync", "staged_async", "blocking_async", "read_only"}`; `TLS` (if set) is one of `{"off", "required"}`. Service-name key matches the same regex as executors.
4. Extend `manifest_test.go`:
   - Add `TestManifest_ValidExecutorsBlock` — manifest with one executor entry, all fields populated, validates OK.
   - Add `TestManifest_ExecutorTransportInvalid` — `transport: foo` fails with substring "transport".
   - Add `TestManifest_ClaimProducerMissingWriteSemantics` — entry without `write_semantics_allowed` fails with "write_semantics_allowed: required".
   - Add `TestManifest_ServiceNameInvalid` — key `Foo` (uppercase) fails the regex.
5. Run `cd cmd && go test ./rimsky/cli/compose/... -run Manifest -count=1` and confirm all manifest tests pass (the existing ones plus the four new).

### Task 2: Add `run` to the compose dispatcher

**Files:** `cmd/rimsky/cli/compose/cmd.go` (mutate), `cmd/rimsky/cli/compose/compose_dispatch_test.go` (mutate)

**Spec reference:** TD-cli-verb.

**Steps:**

1. In `cmd.go::Dispatch`, add a new switch case after the existing `case "status":` block:
   ```go
   case "run":
       return RunComposeRun(ctx, rest)
   ```
2. Update the usage strings in `Dispatch`'s error path and help case to read:
   ```
   usage: rimsky compose <up|down|plan|status|run> ...
   ```
3. In `cmd/rimsky/main.go`, update the `dispatchCompose` doc comment to mention `run` and update `printRootUsage` (or wherever the root help text lives) to include "run" in the compose verb list. (Find the help line by `grep -n 'compose up' cmd/rimsky/main.go`; mirror its format.)
4. Extend `compose_dispatch_test.go`:
   - Add `TestDispatch_RoutesRunToRunComposeRun` — pass `["run", "--help"]`; assert the route reached `RunComposeRun` (use the stub-substitution pattern the existing tests use, or compare exit code).
5. `RunComposeRun` doesn't exist yet — add a stub in a new file `cmd/rimsky/cli/compose/run.go`:
   ```go
   // run.go — `rimsky compose run` one-shot in-process orchestrator.
   package compose

   import (
       "context"
       "fmt"
       "os"
   )

   // RunComposeRun implements `rimsky compose run <manifest>`. Drives a
   // compose manifest to terminal in a self-hosted runtime stack.
   // @story: one-shot-to-terminal
   func RunComposeRun(ctx context.Context, args []string) int {
       fmt.Fprintln(os.Stderr, "rimsky compose run: not yet implemented")
       return 2
   }
   ```
6. Run `cd cmd && go build ./rimsky/...` and confirm clean build.
7. Run `cd cmd && go test ./rimsky/cli/compose/... -run Dispatch -count=1` and confirm dispatch tests pass.

### Task 3: Define the `compose run` flag parser

**Files:** `cmd/rimsky/cli/compose/run.go` (mutate), `cmd/rimsky/cli/compose/run_test.go` (new)

**Spec reference:** TD-cli-verb, TD-timeout-flag, TD-progress-flags, TD-service-spawn-flag, TD-artifact-root-discovery.

**Background.** Model the flag parser on `cmd/rimsky/cli/run.go::parseRunFlags` (the existing `--service` flag etc.) and on `cmd/rimsky/cli/compose/apply.go::parseComposeFlags` (manifest-path positional). The repeatable `--service` flag pattern uses the same `repeatedFlag` value type defined in `cmd/rimsky/cli/run.go#25`; re-use it via export or copy the type into compose/ if not exported.

**Steps:**

1. Verify whether `repeatedFlag` is exported (`grep -n "^type repeatedFlag\|^type RepeatedFlag" cmd/rimsky/cli/`). If not exported, add a package-internal copy to compose/run.go or export the existing one (preferred: export, naming `cli.RepeatedFlag`, since plumbline forbids `@source:` copies of identical types).
2. Define the flags struct:
   ```go
   type composeRunFlags struct {
       manifestPath string
       name         string        // --name
       workdir      string        // --workdir
       timeout      time.Duration // --timeout (0 = unbounded)
       quiet        bool          // --quiet
       verbose      bool          // --verbose
       json         bool          // --json
       services     cli.RepeatedFlag // --service <name>=<path>|<name>
   }
   ```
3. Write `parseComposeRunFlags(args []string) (*composeRunFlags, int)` that uses `flag.NewFlagSet("compose run", flag.ContinueOnError)`. Validate after parse:
   - Exactly one positional arg (the manifest path); non-empty.
   - `--quiet` and `--verbose` are mutually exclusive (return usage error if both set).
4. Replace the stub body of `RunComposeRun` with `flags, code := parseComposeRunFlags(args); if code != 0 { return code }; fmt.Fprintf(os.Stderr, "parsed: %+v\n", flags); return 0`. (This is interim; subsequent passes will replace the println with the launcher wiring.)
5. Add `run_test.go` with table-driven tests:
   - Valid: `["./manifest.yml"]` → no error.
   - Valid with flags: `["--name", "foo", "--timeout", "30m", "--quiet", "./m.yml"]` → fields set correctly.
   - Invalid: no positional → exit 2.
   - Invalid: `--quiet --verbose` together → exit 2 with "mutually exclusive".
   - Multiple `--service`: `["--service", "foo=/bin/x", "--service", "bar", "./m.yml"]` → `flags.services` has two entries.
6. Run `cd cmd && go test ./rimsky/cli/compose/... -run RunComposeRun_FlagParse -count=1`.

### Task 4: Wire `Manifest` validation to fold in sibling `rimsky.yml`

**Files:** `cmd/rimsky/cli/compose/manifest.go` (mutate), `cmd/rimsky/cli/compose/manifest_test.go` (mutate)

**Spec reference:** TD-services-source (publishers and named-locks pass through from sibling rimsky.yml).

**Background.** When `compose run` loads a manifest, it needs to know if a sibling `rimsky.yml` exists for fall-through. Add a loader helper that reads either source.

**Steps:**

1. In `manifest.go`, add a new helper that does NOT load YAML but rather reports the sibling path:
   ```go
   // SiblingRimskyYMLPath returns the absolute path of a sibling
   // rimsky.yml if one exists next to the manifest; empty string
   // otherwise. Used by `compose run` to fold non-services config
   // (publishers, named_locks) through from rimsky.yml.
   func SiblingRimskyYMLPath(manifestPath string) string {
       dir := filepath.Dir(manifestPath)
       candidate := filepath.Join(dir, "rimsky.yml")
       if _, err := os.Stat(candidate); err == nil {
           return candidate
       }
       return ""
   }
   ```
2. Add `TestSiblingRimskyYMLPath_Present` and `TestSiblingRimskyYMLPath_Absent` (use `t.TempDir()` to set up the layouts).
3. Run `cd cmd && go test ./rimsky/cli/compose/... -run Sibling -count=1`.
4. Run `make lint` from repo root and confirm clean. (Plumbline lint will pass since all comments are GoDoc-form or structured-tag.)

---

## Pass 2: Artifact-root discovery + run-directory layout + synthetic config emission

**Goal:** Implement the filesystem layer for `.rimsky/runs/<timestamp>-<name>/` — discovery (walk-up from cwd), run-directory computation (collision-safe), `state.db` / `blobs/` directories, and synthetic `rimsky.yml` / `supervisor.yml` writers. All pure-filesystem operations, no runtime wiring.
**Scope:** Tasks 5–9
**Falsifier:** root discovery does not walk up (passes cwd through unchanged when a parent contains `.rimsky/`); OR the run-dir collision walker is not atomic (two concurrent invocations land on the same directory); OR the synthetic `rimsky.yml` is missing the `executors:` block when `--service` flags were supplied; OR the synthetic `supervisor.yml` differs from the all-in-one baked file (the spec says it inherits defaults verbatim).

### Task 5: Implement artifact-root walk-up discovery

**Files:** `cmd/rimsky/cli/compose/artifact.go` (new), `cmd/rimsky/cli/compose/artifact_test.go` (new)

**Spec reference:** TD-artifact-root-discovery.

**Steps:**

1. Create `cmd/rimsky/cli/compose/artifact.go` with the GoDoc-form file header.
2. Add `DiscoverArtifactRoot(cwd string, workdirOverride string) (string, error)`:
   - If `workdirOverride != ""`: `os.MkdirAll(workdirOverride, 0o755)`, return its absolute path.
   - Otherwise: start at `cwd`, walk parent directories. At each level, `stat <dir>/.rimsky/`. If exists and is a directory, return `<dir>` (the artifact root is the directory *containing* `.rimsky/`, per Behavior step 3 of the spec).
   - Stop at `filepath.VolumeName(cwd)` or root (`/` on Unix). If no `.rimsky/` found, return `cwd` (and the caller creates `.rimsky/` on first run).
3. Add `EnsureRunDir(root, timestamp, name string) (path string, err error)`:
   - Compute `base = filepath.Join(root, ".rimsky", "runs", timestamp+"-"+name)`.
   - Try `os.Mkdir(base, 0o755)`. If success, return `base`. If `errors.Is(err, os.ErrExist)`, retry with `-2`, `-3`, ... up to `-99`. This atomically claims the directory: `os.Mkdir` is an atomic file-system operation (any concurrent walker probing the same path either creates it or fails with `EEXIST`).
   - Also `os.MkdirAll(filepath.Join(base, "blobs"), 0o755)` after the directory is claimed.
4. Add `FormatRunTimestamp(t time.Time) string`:
   - Return `t.UTC().Format("2006-01-02T15-04-05Z")`.
5. Add `artifact_test.go` with:
   - `TestDiscoverArtifactRoot_FindsAncestor` — set up `<tmp>/parent/.rimsky/`, `<tmp>/parent/child/`; cwd `<tmp>/parent/child/` returns `<tmp>/parent`.
   - `TestDiscoverArtifactRoot_StopsAtRoot` — no `.rimsky/` anywhere; cwd `<tmp>/x/` returns `<tmp>/x/` (cwd unchanged).
   - `TestDiscoverArtifactRoot_WorkdirOverride` — override `<tmp>/explicit` is created and returned.
   - `TestEnsureRunDir_Collision` — call twice with same args; second invocation lands on `<base>-2/`.
   - `TestEnsureRunDir_BlobsCreated` — confirm `<runDir>/blobs/` exists post-call.
   - `TestFormatRunTimestamp_FilesystemSafe` — confirm the output matches `^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z$` and contains no colons.
6. Run `cd cmd && go test ./rimsky/cli/compose/... -run Artifact -count=1`.

### Task 6: Implement `latest` symlink update

**Files:** `cmd/rimsky/cli/compose/artifact.go` (mutate), `cmd/rimsky/cli/compose/artifact_test.go` (mutate)

**Spec reference:** TD-artifact-layout.

**Steps:**

1. Add to `artifact.go`:
   ```go
   // UpdateLatestSymlink atomically points <root>/.rimsky/latest at
   // runDir. Uses the symlink+rename pattern (create temp symlink in
   // the same directory, then rename over the target) so a concurrent
   // reader sees either the old target or the new target — never a
   // broken link.
   //
   // @blessed-invariant: latest-symlink-no-broken-window
   func UpdateLatestSymlink(root, runDir string) error { ... }
   ```
   Implementation:
   - Compute `linkPath := filepath.Join(root, ".rimsky", "latest")`.
   - Compute `relTarget`, the runDir relative to `linkPath`'s directory (so the symlink is portable when the root moves).
   - Create a temp symlink in the same directory: `tmpName := fmt.Sprintf("latest.tmp.%d", os.Getpid())`; `os.Symlink(relTarget, filepath.Join(<dir>, tmpName))`.
   - `os.Rename(tmp, linkPath)` — atomic on POSIX.
   - On error, clean up the temp symlink.
2. Add `TestUpdateLatestSymlink_PointsAtTarget` — create a run dir, call update, `os.Readlink(latest)` returns the relative path to the run dir.
3. Add `TestUpdateLatestSymlink_OverwritesExisting` — call twice (two different run dirs); confirm the link points at the second.
4. Add `TestUpdateLatestSymlink_ConcurrentReadersNeverSeeBroken` — spawn 8 goroutines that loop `os.Readlink(latest)` for 500ms; concurrently update the symlink 100 times to alternating targets; assert every readlink succeeded and returned one of the two valid targets (never `os.ErrNotExist`). This is the test that exhibits the no-broken-window property the slug names.
5. **Plumbline pairing**: add `// @blessed-invariant: latest-symlink-no-broken-window — concurrent readers always observe a valid target, never a missing link` as the test file header comment so the plumbline lint's slug scan finds it.
6. Run `cd cmd && go test ./rimsky/cli/compose/... -run UpdateLatestSymlink -count=1 -race`.

### Task 7: Implement synthetic `rimsky.yml` emission

**Files:** `cmd/rimsky/cli/compose/synthetic_config.go` (new), `cmd/rimsky/cli/compose/synthetic_config_test.go` (new)

**Spec reference:** TD-launch-config-injection, TD-persistence-driver, TD-blob-backend, TD-services-source. Behavior step 6.

**Background.** The verb writes `<runDir>/rimsky.yml` for the role runners to read. Shape follows the existing `lib/control/config/stores.go::RimskyYAML` struct.

**Steps:**

1. Create `synthetic_config.go`. Define a serialization struct that mirrors the existing rimsky.yml top-level shape (no need to import config/stores.go's private types; declare local `yaml.Marshal`-ready types):
   ```go
   type syntheticRimskyYAML struct {
       Persistence    syntheticPersistence                       `yaml:"persistence"`
       Executors      map[string]ManifestExecutorEntry           `yaml:"executors,omitempty"`
       ClaimProducers map[string]ManifestClaimProducerEntry      `yaml:"claim_producers,omitempty"`
       // publishers and named_locks pass through from sibling rimsky.yml
       // via a separate path (see Task 8).
   }
   type syntheticPersistence struct {
       Driver string                       `yaml:"driver"`
       SQLite syntheticPersistenceSQLite   `yaml:"sqlite"`
       Blob   syntheticPersistenceBlob     `yaml:"blob"`
   }
   type syntheticPersistenceSQLite struct{ Path string `yaml:"path"` }
   type syntheticPersistenceBlob struct {
       Backend             string                     `yaml:"backend"` // "filesystem"
       Filesystem          syntheticBlobFilesystem    `yaml:"filesystem"`
       SpillThresholdBytes int                        `yaml:"spill_threshold_bytes,omitempty"`
   }
   type syntheticBlobFilesystem struct{ Root string `yaml:"root"` }
   ```
2. Add `WriteSyntheticRimskyYAML(runDir string, m *Manifest, mergedExecutors map[string]ManifestExecutorEntry) error`:
   - Build the struct: `Driver: "sqlite"`, `SQLite.Path: filepath.Join(runDir, "state.db")`, `Blob.Backend: "filesystem"`, `Blob.Filesystem.Root: filepath.Join(runDir, "blobs")`.
   - Set `Executors = mergedExecutors` (the caller is responsible for merging manifest entries + spawned-service entries; see Task 14).
   - Set `ClaimProducers = m.ClaimProducers`.
   - `yaml.Marshal` (use `gopkg.in/yaml.v3` — confirm the project's existing yaml import path via `grep -rn '"gopkg.in/yaml' cmd/rimsky/cli/compose/manifest.go`).
   - Write to `<runDir>/rimsky.yml` with `0o644`.
3. Add `synthetic_config_test.go`:
   - `TestWriteSyntheticRimskyYAML_PathsCorrect` — call with `runDir="/tmp/foo"`; load the result back via `LoadRimskyConfigYAML`; assert `persistence.driver == "sqlite"`, `persistence.sqlite.path == "/tmp/foo/state.db"`, `persistence.blob.backend == "filesystem"`, `persistence.blob.filesystem.root == "/tmp/foo/blobs"`.
   - `TestWriteSyntheticRimskyYAML_MergedExecutors` — pass `mergedExecutors = {"foo": {Transport: "grpc", Endpoint: "127.0.0.1:9091"}}`; load back; assert the executor appears.
   - `TestWriteSyntheticRimskyYAML_ClaimProducersFromManifest` — manifest with one claim_producer; result file carries it.
4. Run `cd cmd && go test ./rimsky/cli/compose/... -run WriteSyntheticRimskyYAML -count=1`.

### Task 8: Implement synthetic `supervisor.yml` emission

**Files:** `cmd/rimsky/cli/compose/synthetic_config.go` (mutate), `cmd/rimsky/cli/compose/synthetic_config_test.go` (mutate)

**Spec reference:** TD-launch-config-injection. Behavior step 6 (the supervisor.yml inherits the all-in-one defaults verbatim).

**Steps:**

1. Read the all-in-one supervisor baked file: `cat dockerfiles/all-in-one.supervisor-config.yml`. The verb inherits these defaults verbatim (per spec):
   ```yaml
   concurrency: 8
   heartbeat_interval_ms: 5000
   claim_poll_interval_ms: 1000
   callback:
     host: 0.0.0.0
     port: 9100
     advertise_host: 127.0.0.1
   ```
2. Add to `synthetic_config.go`:
   ```go
   // syntheticSupervisorYAML defaults are derived verbatim from the
   // all-in-one baked file at dockerfiles/all-in-one.supervisor-config.yml.
   // The compose-run verb writes these inline rather than referencing the
   // baked file so a built CLI binary is self-contained.
   const syntheticSupervisorYAML = `concurrency: 8
   heartbeat_interval_ms: 5000
   claim_poll_interval_ms: 1000
   callback:
     host: 0.0.0.0
     port: 9100
     advertise_host: 127.0.0.1
   `

   func WriteSyntheticSupervisorYAML(runDir string) error {
       return os.WriteFile(filepath.Join(runDir, "supervisor.yml"), []byte(syntheticSupervisorYAML), 0o644)
   }
   ```
3. Add `TestWriteSyntheticSupervisorYAML_MatchesBakedDefault` — write, then byte-compare against the bytes of `dockerfiles/all-in-one.supervisor-config.yml` from the repo root. (Use `filepath.Walk` to find the repo root, or hardcode a `../../../../dockerfiles/all-in-one.supervisor-config.yml` relative path; document the assumed cwd.)
4. Run `cd cmd && go test ./rimsky/cli/compose/... -run WriteSyntheticSupervisorYAML -count=1`.

### Task 9: Plumbline lint sanity

**Files:** no edits.

**Steps:**

1. Run `plumbline ./cmd/rimsky/cli/compose/` from repo root and confirm clean exit on the files Pass 1–2 created/touched.
2. Run `make lint` from repo root and confirm clean.

---

## Pass 3: Spawn-primitive extraction (strict DRY)

**Goal:** Carve the host-agent's exec + ready-poll spawn mechanism into a reusable helper. Both the host-agent daemon (its existing `handleSpawn` call site) and the compose-run verb (the upcoming wiring) call the same primitive — no `@source:` copy. Plumbline's strict DRY rule mandates this is a single definition.
**Scope:** Tasks 10–12
**Falsifier:** the helper function exists but `lib/runtime/hostagent/spawn.go::handleSpawn` no longer goes through it (silent copy left behind); OR the host-agent's existing test suite breaks (extraction changed behavior); OR the helper's API requires the gRPC `Spawn` proto type (couples it to the host-agent's wire surface, defeats reuse).

### Task 10: Extract `SpawnService` from `handleSpawn`

**Files:** `lib/runtime/hostagent/spawn.go` (mutate)

**Spec reference:** TD-service-spawn-flag. Behavior step 5.

**Background.** Today `handleSpawn` (`lib/runtime/hostagent/spawn.go::61`) takes a `*genv1.Spawn` proto, validates allowed paths via `a.pathAllowed`, picks a port via `freeLocalPort`, sets `RIMSKY_AGENT_PORT`, execs the child, waits with `waitPortReady`, performs a capabilities handshake, and registers the child in the agent's tracker. We extract the **mechanical part** (port-pick → env-injection → exec → ready-poll) into a standalone primitive that takes Go-native arguments. The capabilities handshake and agent-side child tracking stay in `handleSpawn` (they're host-agent-specific). All extracted code lives in `spawn.go` alongside the existing host-agent code — no new file — so the strict-DRY relationship between caller (handleSpawn) and helper (SpawnService) is co-located and visible.

**Steps:**

1. Read the current `handleSpawn` body in full so the extraction preserves behavior exactly.
2. Export `freeLocalPort` as `FreeLocalPort` (Pass 4's launcher pre-picks a port for the control-api via this helper). The function signature stays `func FreeLocalPort() (int, error)`; update all internal callers in the same edit.
3. Define the primitive in the same package:
   ```go
   // SpawnServiceParams configures one binary spawn for late-bound
   // service hosting.
   type SpawnServiceParams struct {
       BinaryPath   string        // absolute path to the binary to exec
       Env          []string      // base env; SpawnService appends RIMSKY_AGENT_PORT
       ReadyTimeout time.Duration // bound on poll-dial; defaults to 30s when 0
   }

   // SpawnedService is the handle returned by SpawnService.
   type SpawnedService struct {
       Cmd  *exec.Cmd // the running child; caller owns lifecycle
       Port int       // localhost port the child bound
   }

   // SpawnService picks a free localhost port, exec()s BinaryPath with
   // RIMSKY_AGENT_PORT set in its environment, and poll-dials
   // 127.0.0.1:<port> until the child binds a TCP listener there or
   // the ReadyTimeout elapses. On readiness timeout, the child is
   // killed and reaped before the function returns (no leak).
   //
   // @agent-contract guarantees: returns nil error iff the child
   //   process is running, its port is reachable on 127.0.0.1, and
   //   the caller owns its lifecycle (must call Cmd.Process.Kill or
   //   await Cmd.Wait). Does NOT perform any capabilities handshake
   //   or protocol registration — that is the caller's concern.
   //
   // @blessed-invariant: spawn-no-leak-on-readiness-timeout
   func SpawnService(ctx context.Context, params SpawnServiceParams) (*SpawnedService, error) { ... }
   ```
4. Implementation:
   - Port-pick: call `FreeLocalPort` (exported in step 2).
   - Build env: copy `params.Env`, append `fmt.Sprintf("%s=%d", agentPortEnvVar, port)`.
   - Default `ReadyTimeout` to 30s when zero.
   - `exec.CommandContext(ctx, params.BinaryPath)` with the constructed env; `Start()`.
   - On `Start` error, return wrapped.
   - Goroutine: `cmd.Wait()` → close an `exited` chan with the result. Mirror the existing `handleSpawn` pattern at line 109-113.
   - Call `waitPortReady(ctx, port, exited, params.ReadyTimeout)`. If false (timeout or early exit):
     - `_ = cmd.Process.Kill(); _ = cmd.Wait()` — reap.
     - Return `fmt.Errorf("child did not bind port %d within %s", port, params.ReadyTimeout)`.
   - On success: return `&SpawnedService{Cmd: cmd, Port: port}, nil`.
5. Update `handleSpawn` to call `SpawnService` for the port-pick + exec + ready-poll. Keep the proto unmarshalling, allowed-path check, env construction from `sp.GetEnvAppend()`, capabilities handshake, and child registration where they are. Roughly:
   ```go
   func (a *agent) handleSpawn(ctx context.Context, sp *genv1.Spawn) *genv1.SpawnAck {
       if !a.pathAllowed(sp.GetBinaryPath()) { ... }
       env := append(os.Environ(), sp.GetEnvAppend()...)
       result, err := SpawnService(ctx, SpawnServiceParams{
           BinaryPath:   sp.GetBinaryPath(),
           Env:          env,
           ReadyTimeout: time.Duration(sp.GetReadyTimeoutSeconds()) * time.Second,
       })
       if err != nil { return spawnFailed(sp.GetSpawnId(), err.Error()) }
       // existing capabilities handshake against result.Port
       // existing child registration with result.Cmd
       ...
   }
   ```
6. Plumbline strict-DRY check: `grep -rn "FreeLocalPort\|waitPortReady" lib/runtime/hostagent/` — confirm each helper has exactly one definition. No `@source:` copies introduced.

### Task 11: Tests for the new primitive

**Files:** `lib/runtime/hostagent/spawn_helper_test.go` (new, OR extend `spawn_test.go`)

**Spec reference:** TD-service-spawn-flag.

**Steps:**

1. Build a test fixture binary: `lib/runtime/hostagent/testdata/stub-service/main.go` that reads `RIMSKY_AGENT_PORT`, binds a TCP listener there, and serves a trivial gRPC server (or just an `http.Server` that returns 200 on `/`). Build target via `go build -o <tmp>/stub-service ./testdata/stub-service` in a `TestMain` setup.
2. Add `TestSpawnService_HappyPath`: spawn the stub, assert no error, dial `127.0.0.1:<port>`, kill, assert `cmd.Wait()` returns.
3. Add `TestSpawnService_ReadyTimeoutReapsChild`: build a fixture binary that just `time.Sleep(60*time.Second)` (never binds). Call `SpawnService` with `ReadyTimeout: 200*time.Millisecond`. Assert: error returned; check via `ps` or `os.FindProcess` that the child PID is gone (or use `cmd.ProcessState.Exited()` after `cmd.Wait`).
4. Add `// @blessed-invariant: spawn-no-leak-on-readiness-timeout — reaping check` comment header to the readiness-timeout test so plumbline pairs it.
5. Run `go test ./lib/runtime/hostagent/... -count=1 -race`.

### Task 12: Verify host-agent still passes its existing tests

**Files:** none (verification only).

**Steps:**

1. Run `go test ./lib/runtime/hostagent/... -count=3 -race` (existing tests, race-detected, three iterations to catch flakes since this is a concurrency-touching refactor).
2. Run `go test ./test/scenarios/... -count=1` for any host-agent-bearing scenarios — confirm clean.
3. Run `make lint` from repo root.
4. Run `plumbline ./lib/runtime/hostagent/` — clean exit.

---

## Pass 4: In-process launcher + migration + control-api readiness + compose-engine `TerminateAfterRun` hook

**Goal:** Bring up the unified rimsky runtime stack in-process from `compose run`. Implement migration-direct, the three role-runner orchestration site, the loopback control-api readiness poll, and the small compose-engine extension that sets `TerminateAfterRun=true` on every CreateInstance.
**Scope:** Tasks 13–14
**Falsifier:** the verb starts but the unified stack never accepts requests (control-api binding fails or readiness never resolves); OR migrations are not run before the role runners start (a fresh run hits "no such table" errors); OR `ApplyOpts.TerminateAfterRun` does not thread to `body.TerminateAfterRun` on the CreateInstance request; OR the role runners are started in the wrong order so the supervisor opens a transaction before migrations land.

### Task 13: Extend `ApplyOpts` with `TerminateAfterRun`

**Files:** `cmd/rimsky/cli/compose/apply.go` (mutate), `cmd/rimsky/cli/compose/apply_test.go` (mutate)

**Spec reference:** TD-instance-self-termination.

**Background.** Today `apply.go::ApplyOpts` carries only `Logger io.Writer`. The existing `applyStep` for `ActionInstanceCreate` builds `cli.CreateInstanceRequest{Template, InstanceKey, Params}` — does not set `TerminateAfterRun`. We add the field to `ApplyOpts`, pass `opts` through to `applyStep`, and set the request body field when the option is true.

**Steps:**

1. Modify `ApplyOpts`:
   ```go
   type ApplyOpts struct {
       Logger io.Writer
       // TerminateAfterRun, when true, sets terminate_after_run=true
       // on every CreateInstance the engine issues. Used by compose-run
       // so manifest-declared instances self-terminate after their
       // first run reaches terminal. The existing up/down/plan/status
       // verbs leave this false (durable instances are the default).
       //
       // @decision: instance-self-termination
       TerminateAfterRun bool
   }
   ```
2. Modify `applyStep` signature to take `opts ApplyOpts` (currently takes the bare logger writer). Update the `ApplyPlan` loop to pass `opts` through.
3. In `applyStep` for `ActionInstanceCreate`:
   ```go
   case ActionInstanceCreate:
       key := step.InstanceKey
       body := cli.CreateInstanceRequest{
           Template:    step.TemplateTag,
           InstanceKey: &key,
           Params:      step.Params,
       }
       if opts.TerminateAfterRun {
           body.TerminateAfterRun = true
       }
       if _, err := c.CreateInstance(ctx, body); err != nil { return err }
       logf("create", step.InstanceKey, "ok")
   ```
4. Add `TestApplyPlan_TerminateAfterRunPropagates` in apply_test.go: use a fake `cli.Client` (or http test server) that captures the CreateInstance request body; pass `ApplyOpts{TerminateAfterRun: true}`; assert the body's `terminate_after_run` JSON field is `true`. With `ApplyOpts{}` (the default), assert it's absent/false.
5. Run `cd cmd && go test ./rimsky/cli/compose/... -run ApplyPlan -count=1`.
6. Verify the existing `up`/`down`/`plan`/`status` callers still compile by running `cd cmd && go build ./rimsky/cli/compose/...`. The callers don't pass `TerminateAfterRun`, so the default false preserves the existing durable-instance behavior — no behavior change for them.

### Task 14: Implement the in-process role-runner orchestrator and migration call

**Files:** `cmd/rimsky/cli/compose/launcher.go` (new), `cmd/rimsky/cli/compose/launcher_test.go` (new)

**Spec reference:** TD-launch-integration, TD-launch-config-injection, TD-migration-direct, TD-network-binding. Behavior steps 7–9.

**Background.** Model on `cmd/rimsky-entrypoint/main.go::runUnified` (lines 201–268). The runners are `launch.RunScheduler`, `launch.RunSupervisor`, `launch.RunControlAPI`, each returning `(StopFunc, <-chan error, error)`. We add migration-direct (calling `Database.Migrate` against the freshly-created sqlite db). The package is `lib/foundation/persistence`; the open function is `code:lib/foundation/persistence/open.go::Open` with signature `func Open(ctx context.Context, cfg Config) (Database, error)`. The `Database` interface exposes `Migrate(ctx context.Context, logger shared.Logger) error` — note the logger is `shared.Logger` (the project-internal interface from `code:lib/foundation/shared/logger.go`), NOT `*slog.Logger`. Wrap with `shared.NewSlogLogger(logger)` at the call site.

**Pre-step — control-api port resolution (folds Task 15 here):** The role-runner `launch.RunControlAPI` reads `env:RIMSKY_CONTROL_API_HOST` (default `127.0.0.1`) and `env:RIMSKY_CONTROL_API_PORT` (default `8080`; an explicit `"0"` is mapped back to `8080` per the file header, NOT kernel-picked). So the verb cannot rely on `:0` — it must **pre-pick** a free port via the same `freeLocalPort` helper Pass 3 exposes, then set `RIMSKY_CONTROL_API_HOST=127.0.0.1` and `RIMSKY_CONTROL_API_PORT=<picked>` on the in-process env before `StartRoleStack` runs. The resolved endpoint `http://127.0.0.1:<picked>` is exposed via `RoleStack.Endpoint()`.

**Steps:**

1. Pre-pick a free port via `hostagent.FreeLocalPort()` (export the existing helper as part of Pass 3 if not already). Set the env vars in this task before constructing the stack so `RunControlAPI` honors them.
2. Create `launcher.go`:
   ```go
   // launcher.go — in-process role-runner orchestrator for `compose run`.
   //
   // Models cmd/rimsky-entrypoint/main.go::runUnified. Starts the three
   // role runners in order, tracks their stop functions, selects on a
   // combined signal-or-failure channel, drains in reverse order.
   package compose

   import (
       "context"
       "fmt"
       "log/slog"
       "net/http"
       "time"

       "github.com/rimsky-ai/rimsky-core/lib/control/launch"
       "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
       // import path for the config loader
       "github.com/rimsky-ai/rimsky-core/lib/control/config"
   )

   type RoleStack struct {
       stops    []launch.StopFunc
       failCh   chan roleFailure
       endpoint string // resolved loopback endpoint after start
   }

   type roleFailure struct { role string; err error }

   // StartRoleStack runs migrations against the synthetic config, then
   // starts the three role runners (scheduler, supervisor, control-api)
   // in order. RIMSKY_CONFIG, RIMSKY_SUPERVISOR_CONFIG, and
   // RIMSKY_PROCESS_ROLE must be set in the process env before this is
   // called.
   //
   // @blessed-invariant: migrations-run-before-runners
   func StartRoleStack(ctx context.Context, logger *slog.Logger, configPath string, endpoint string) (*RoleStack, error) {
       cfg, err := config.LoadRimskyConfigYAML(configPath)
       if err != nil { return nil, fmt.Errorf("load synthetic rimsky.yml: %w", err) }

       db, err := persistence.Open(ctx, cfg.Persistence)
       if err != nil { return nil, fmt.Errorf("open persistence: %w", err) }
       if err := db.Migrate(ctx, shared.NewSlogLogger(logger)); err != nil {
           _ = db.Close()
           return nil, fmt.Errorf("migrate: %w", err)
       }
       // @deliberate: close-then-reopen — role runners each call
       // persistence.Open themselves on startup; sqlite's per-driver
       // advisory-lock model permits sequential reopen of the same
       // file. Migrate must run before any runner opens to avoid
       // schema-missing errors on first transaction.
       _ = db.Close()

       runners := []struct {
           name string
           run  func(context.Context, *slog.Logger) (launch.StopFunc, <-chan error, error)
       }{
           {"scheduler", launch.RunScheduler},
           {"supervisor", launch.RunSupervisor},
           {"control-api", launch.RunControlAPI},
       }
       stack := &RoleStack{failCh: make(chan roleFailure, len(runners))}
       for _, r := range runners {
           stop, failCh, err := r.run(ctx, logger.With("role", r.name))
           if err != nil {
               stack.Drain(context.Background(), 5*time.Second)
               return nil, fmt.Errorf("start %s: %w", r.name, err)
           }
           stack.stops = append(stack.stops, stop)
           go func(name string, ch <-chan error) {
               if err, ok := <-ch; ok && err != nil {
                   stack.failCh <- roleFailure{role: name, err: err}
               }
           }(r.name, failCh)
       }
       stack.endpoint = endpoint
       return stack, nil
   }

   // Drain stops the role runners in reverse order, bounded by deadline.
   func (s *RoleStack) Drain(ctx context.Context, deadline time.Duration) {
       drainCtx, cancel := context.WithTimeout(ctx, deadline)
       defer cancel()
       for i := len(s.stops) - 1; i >= 0; i-- { _ = s.stops[i](drainCtx) }
   }

   // FailCh exposes the role-failure channel for the caller's select loop.
   func (s *RoleStack) FailCh() <-chan roleFailure { return s.failCh }

   // Endpoint returns the loopback URL the control-api bound to,
   // pre-computed by the caller from the picked port.
   func (s *RoleStack) Endpoint() string { return s.endpoint }
   ```
3. Add `WaitForControlAPIReady(ctx context.Context, endpoint string, deadline time.Duration) error` that polls `<endpoint>/v1/health` until it returns 200 or the deadline expires.
4. Verify all import paths and symbol names by `rg`. Adjust the skeleton above to match actual signatures (`launch.StopFunc` shape, `config.LoadRimskyConfigYAML` signature, `persistence.Open` already verified above).
5. Add `launcher_test.go`:
   - `TestStartRoleStack_BootsAndDrains` — pre-pick a port via `hostagent.FreeLocalPort()`; write a synthetic rimsky.yml + supervisor.yml to a tempdir; set `RIMSKY_CONTROL_API_HOST=127.0.0.1`, `RIMSKY_CONTROL_API_PORT=<picked>`, `RIMSKY_CONFIG`, `RIMSKY_SUPERVISOR_CONFIG`, `RIMSKY_PROCESS_ROLE=unified`; call `StartRoleStack`; assert no error; call `Drain(ctx, 5*time.Second)`; assert the drain returns without panic.
   - `TestMigrationsRunBeforeRunners_BI` — write a minimal rimsky.yml; call `StartRoleStack`; query `state.db` directly via `database/sql` (sqlite driver) and assert the migrations bookkeeping table has rows. Add `// @blessed-invariant: migrations-run-before-runners — migrations apply against fresh DB before any role runner opens it` as a file-level comment so the plumbline lint pairs slug to test.
   - `TestWaitForControlAPIReady_Polls` — start a test http server that returns 503 for the first 300ms then 200. Call `WaitForControlAPIReady` with a 2-second deadline. Assert success.
6. Run `cd cmd && go test ./rimsky/cli/compose/... -run RoleStack -count=1 -race`. Race-detected to catch goroutine races in the failure-channel select.

---

## Pass 5: `--service` spawn integration, terminal-wait loop, progress printer, exit codes, graceful drain

**Goal:** Wire all of Passes 1–4 together inside `RunComposeRun`. The verb now does the full job: discover, spawn services, write configs, boot stack, apply manifest, watch instances, print progress, exit cleanly.
**Scope:** Tasks 16–21
**Falsifier:** the verb returns before all declared instances reach terminal (silent under-completion); OR a spawned `--service` child binary persists after the verb exits (visible as a stray process); OR progress lines appear only after the run finishes (buffered, not line-flushed); OR exit code is 0 when an instance terminated with failure; OR a second SIGINT during shutdown does not escalate to hard exit.

### Task 16: Implement progress printer

**Files:** `cmd/rimsky/cli/compose/progress.go` (new), `cmd/rimsky/cli/compose/progress_test.go` (new)

**Spec reference:** TD-progress-default, TD-progress-flags. STORY-live-progress.

**Steps:**

1. Define `ProgressPrinter` interface and three implementations: `defaultPrinter` (per-node lifecycle), `quietPrinter` (final summary only), `verbosePrinter` (extends default with frame ticks + claim events). Plus a `--json` mode that wraps any of the above to emit JSON Lines.
2. Each printer exposes:
   ```go
   type ProgressPrinter interface {
       InstanceStarting(project, name string)
       NodeRunTerminal(project, name, nodeID, outcome, reason string)
       InstanceTerminal(project, name, outcome string, frames int)
       FrameTick(project, name string, frameNo int)   // verbose only; no-op for default/quiet
       Finalize(w io.Writer)                          // optional aggregate summary
   }
   ```
3. The default printer writes to a `bufio.Writer` over `os.Stderr` and calls `Flush()` after each line (line-buffered, no batching).
4. The JSON wrapper emits one JSON object per line, e.g. `{"event":"node_terminal","project":"foo","instance":"bar","node":"baz","outcome":"success"}`.
5. `progress_test.go`:
   - `TestDefaultPrinter_LineFlushed` — capture output to a `bytes.Buffer`; assert each call produces one line, terminated by `\n`, present in the buffer immediately (no batching).
   - `TestQuietPrinter_SuppressesEvents` — `InstanceStarting` and `NodeRunTerminal` produce no output.
   - `TestVerbosePrinter_EmitsFrameTicks` — `FrameTick` produces a line; default printer's `FrameTick` does not.
   - `TestJSONPrinter_EmitsJSONLines` — each line `json.Unmarshal`s into a map with the expected keys.
6. Run `cd cmd && go test ./rimsky/cli/compose/... -run Printer -count=1`.

### Task 17: Implement terminal-wait loop

**Files:** `cmd/rimsky/cli/compose/wait.go` (new), `cmd/rimsky/cli/compose/wait_test.go` (new)

**Spec reference:** TD-termination, TD-instance-self-termination. Behavior step 11.

**Steps:**

1. Define `WaitForInstancesTerminal(ctx context.Context, client *cli.Client, instanceIDs []string, printer ProgressPrinter, pollInterval time.Duration) (outcomes map[string]string, err error)`:
   - Poll each instance via `client.GetInstance(ctx, id)` (or whatever the existing read endpoint is — verify via `grep -n "GetInstance\|InstanceStatus" cmd/rimsky/cli/client.go`).
   - On state transition (new terminal), call `printer.NodeRunTerminal` (per-node) or `printer.InstanceTerminal` (per-instance).
   - When all instances are terminal, return.
   - Default `pollInterval = 250 * time.Millisecond`. Existing `rimsky run --no-keep` uses 1s; we go shorter for live progress feel.
2. Outcome enum: `"success" | "failure" | "parked-timeout"`. Map from terminal-state strings the control-api returns.
3. `wait_test.go`:
   - `TestWaitForInstancesTerminal_ReturnsOnAllTerminal` — fake client that returns "running" for first poll, "success" for second; assert returns with `{id: "success"}`.
   - `TestWaitForInstancesTerminal_CallsPrinter` — assert `InstanceTerminal` was called for each id with correct outcome.
   - `TestWaitForInstancesTerminal_ContextCancelExits` — cancel ctx; assert returns with `ctx.Err()` and not a poll-loop hang.
4. Run `cd cmd && go test ./rimsky/cli/compose/... -run WaitForInstancesTerminal -count=1`.

### Task 18: Implement graceful drain coordinator

**Files:** `cmd/rimsky/cli/compose/shutdown.go` (new), `cmd/rimsky/cli/compose/shutdown_test.go` (new)

**Spec reference:** TD-graceful-shutdown, TD-exit-codes. Behavior "Graceful shutdown" subsection.

**Steps:**

1. Define a coordinator struct that owns: the role stack (from Task 14), the spawned-service handles (from Task 20), the signal channel, and a shutdown deadline.
2. Method `Drain(ctx context.Context, reason ShutdownReason) (exitCode int)`:
   - Stop accepting new work (the role stack's supervisor has its own stop signal — drain reverses-order via `RoleStack.Drain`).
   - SIGTERM all spawned children: `for _, s := range services { _ = s.Cmd.Process.Signal(syscall.SIGTERM) }`.
   - Wait up to 5s for children to exit. Use `select` over `cmd.Wait()` channels + `time.After(5*time.Second)`.
   - SIGKILL any child still running: `_ = s.Cmd.Process.Kill()`.
   - Call `stack.Drain(ctx, 5*time.Second)` to stop the role runners.
   - Return the exit code per `reason`:
     - `ReasonAllSuccess` → 0
     - `ReasonAnyFailure` → 1
     - `ReasonTimeout` → 2
     - `ReasonSignal` → 130
3. Handle the "second SIGINT during shutdown" escalation: install a second-signal handler that exits immediately (`os.Exit(130)`) after SIGKILL'ing children. This is the safety valve.
4. **Plumbline invariant**: spawn-child-reaped-on-verb-exit. Add `// @blessed-invariant: spawn-child-reaped-on-exit` annotation on the coordinator type.
5. `shutdown_test.go`:
   - `TestDrain_SIGTERMThenSIGKILLChildren_BoundedTime` — spawn a stub binary that ignores SIGTERM (use `signal.Notify` then `time.Sleep` — fixture under `testdata/`); record `start := time.Now()`; call `Drain`; assert the child PID is gone (signal-0 check returns process-not-found) AND `time.Since(start) < 7*time.Second` (5s grace + 2s slack). The duration bound is the load-bearing claim — without it, the test would pass even if drain ran 60s.
   - `TestDrain_AllSuccessReturnsZero` — pass `ReasonAllSuccess` with no spawns; assert returns 0.
   - `TestDrain_AnyFailureReturnsOne` — pass `ReasonAnyFailure`; assert returns 1.
   - `TestDrain_SignalReturnsOneThirty` — pass `ReasonSignal`; assert returns 130.
6. Comment header on `TestDrain_SIGTERMThenSIGKILLChildren_BoundedTime`: `// @blessed-invariant: spawn-child-reaped-on-exit — SIGTERM-then-SIGKILL drain bounded by the hardcoded grace window`.
7. Run `cd cmd && go test ./rimsky/cli/compose/... -run Drain -count=1`.

### Task 19: Wire `RunComposeRun` end-to-end

**Files:** `cmd/rimsky/cli/compose/run.go` (mutate)

**Spec reference:** Behavior steps 1–11.

**Steps:**

1. Replace the interim body of `RunComposeRun` with the full sequence:
   ```go
   func RunComposeRun(ctx context.Context, args []string) int {
       flags, code := parseComposeRunFlags(args)
       if code != 0 { return code }

       // JSON handler over stderr — Task 26's executable proof parses
       // stderr for the `spawned service` event to capture child PIDs.
       logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

       // 2. Load + validate manifest
       m, err := LoadManifest(flags.manifestPath)
       if err != nil { fmt.Fprintln(os.Stderr, err); return 2 }
       resolveTemplatePaths(m, flags.manifestPath)

       // 3-4. Discover artifact root + compute run dir
       cwd, _ := os.Getwd()
       root, err := DiscoverArtifactRoot(cwd, flags.workdir)
       if err != nil { ... return 2 }
       name := flags.name
       if name == "" { name = m.Project }
       runDir, err := EnsureRunDir(root, FormatRunTimestamp(time.Now()), name)
       if err != nil { ... return 2 }

       // 5. Spawn --service binaries (Task 20 fills this in)
       services, mergedExecutors, err := spawnServices(ctx, flags.services, m)
       if err != nil { ... reapAll(services); return 2 }

       // 6-7. Write synthetic configs + run migrations (Task 14)
       if err := WriteSyntheticRimskyYAML(runDir, m, mergedExecutors); err != nil { ... }
       if err := WriteSyntheticSupervisorYAML(runDir); err != nil { ... }

       // 8. Boot role runners — pre-pick control-api port, set env vars, then start.
       controlAPIPort, err := hostagent.FreeLocalPort()
       if err != nil { reapAll(services); return 2 }
       endpoint := fmt.Sprintf("http://127.0.0.1:%d", controlAPIPort)
       os.Setenv("RIMSKY_CONFIG", filepath.Join(runDir, "rimsky.yml"))
       os.Setenv("RIMSKY_SUPERVISOR_CONFIG", filepath.Join(runDir, "supervisor.yml"))
       os.Setenv("RIMSKY_PROCESS_ROLE", "unified")
       os.Setenv("RIMSKY_CONTROL_API_HOST", "127.0.0.1")
       os.Setenv("RIMSKY_CONTROL_API_PORT", strconv.Itoa(controlAPIPort))
       stack, err := StartRoleStack(ctx, logger, filepath.Join(runDir, "rimsky.yml"), endpoint)
       if err != nil { reapAll(services); return 2 }
       defer stack.Drain(context.Background(), 5*time.Second)

       // 9. Wait for control-api ready
       if err := WaitForControlAPIReady(ctx, stack.Endpoint(), 10*time.Second); err != nil { ... }

       // 10. Apply manifest with TerminateAfterRun=true
       c := cli.NewClient(stack.Endpoint())
       c.SetComposeOrigin(true)
       state, err := QueryState(ctx, c, m.Project)
       if err != nil { ... }
       plan, err := ComputePlan(ctx, c, m, state)
       if err != nil { ... }
       printer := newPrinter(flags.quiet, flags.verbose, flags.json)
       created, err := ApplyPlan(ctx, c, plan, ApplyOpts{Logger: stderrWriter, TerminateAfterRun: true})
       if err != nil { ... }

       // 11. Terminal-wait + signal handling
       instanceIDs := extractInstanceIDs(plan)
       sigCh := make(chan os.Signal, 2)
       signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

       coord := &ShutdownCoordinator{
           Stack: stack, Services: services, Logger: logger,
       }

       outcomes, waitErr := WaitForInstancesTerminal(ctx, c, instanceIDs, printer, 250*time.Millisecond)

       var reason ShutdownReason
       switch {
       case waitErr != nil:
           reason = ReasonTimeout // or ReasonSignal if sigCh fired; thread through
       case anyFailed(outcomes):
           reason = ReasonAnyFailure
       default:
           reason = ReasonAllSuccess
       }
       _ = UpdateLatestSymlink(root, runDir)
       return coord.Drain(context.Background(), reason)
   }
   ```
2. Implement `spawnServices(ctx, services cli.RepeatedFlag, m *Manifest, logger *slog.Logger) (handles []*SpawnedService, mergedExecutors map[string]ManifestExecutorEntry, err error)`:
   - For each `--service` entry, parse `<name>=<path>` or bare `<name>` (alias resolution per `cmd/rimsky/cli/run.go::resolveServiceBindings`).
   - Call `hostagent.SpawnService(ctx, SpawnServiceParams{BinaryPath: path, Env: os.Environ(), ReadyTimeout: 30*time.Second})`.
   - On success, register `endpoint: 127.0.0.1:<port>` in mergedExecutors under the service name (overriding any manifest entry with the same name).
   - **Log one structured event per spawn** at info level: `logger.Info("spawned service", "name", name, "path", path, "pid", handle.Cmd.Process.Pid, "port", handle.Port)`. The PID is the observable surface Pass 6 acceptance tests use to verify no-leak (see Task 26).
   - On any error: reap all already-spawned handles, return error.
3. Implement `extractInstanceIDs(plan *Plan) []string` — walk plan.Steps for ActionInstanceCreate steps and collect their resulting IDs. (Note: the plan steps as defined know the InstanceKey but not the server-assigned ID. The verb likely needs to capture IDs from CreateInstance responses; alternatively, list instances by tag after apply. Verify the simplest path by reading the existing `up` flow.)
4. Hook up SIGINT/SIGTERM handling. The signal handler runs Drain(ReasonSignal). A second signal during Drain hard-exits.
5. Run `cd cmd && go build ./rimsky/...` (full build) and confirm clean compilation.

### Task 20: Add `extractInstanceIDs` resolution

**Files:** `cmd/rimsky/cli/compose/run.go` (mutate, OR `cmd/rimsky/cli/compose/apply.go` to extend `ApplyPlan` to return created IDs)

**Spec reference:** Behavior step 11 (terminal-wait).

**Steps:**

1. Inspect the existing CreateInstance response (`cmd/rimsky/cli/client.go::CreateInstance`). It returns `(*CreateInstanceResponse, error)`; the response carries the ID.
2. The cleanest hook: extend `ApplyPlan` to return a `[]CreatedInstance` slice. Modify the signature:
   ```go
   func ApplyPlan(ctx context.Context, c *cli.Client, plan *Plan, opts ApplyOpts) (created []CreatedInstance, err error)
   type CreatedInstance struct { Key, ID string }
   ```
   In `applyStep`'s `ActionInstanceCreate` case, capture the returned ID and append to a slice owned by ApplyPlan.
3. Update the existing `RunComposeUp` and `RunComposeDown` callers — they ignore the new return value or destructure with `_`. Verify by `grep -n "ApplyPlan(" cmd/rimsky/cli/compose/`.
4. `RunComposeRun` then has `created` directly:
   ```go
   created, err := ApplyPlan(ctx, c, plan, ApplyOpts{TerminateAfterRun: true, ...})
   if err != nil { ... }
   instanceIDs := make([]string, 0, len(created))
   for _, ci := range created { instanceIDs = append(instanceIDs, ci.ID) }
   ```
5. Update apply_test.go's existing tests to handle the new return value (just `_, err := ApplyPlan(...)` where they don't care).
6. Run `cd cmd && go test ./rimsky/cli/compose/... -count=1`. Run `cd cmd && go build ./rimsky/...` clean.

### Task 21: Hook signal handling and second-SIGINT escalation into `RunComposeRun`

**Files:** `cmd/rimsky/cli/compose/run.go` (mutate)

**Spec reference:** TD-graceful-shutdown (second SIGINT → hard exit).

**Steps:**

1. The standard pattern: `sigCh` is buffered (`make(chan os.Signal, 2)`); `signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)`. The first signal triggers the drain path; the second signal during drain triggers immediate `os.Exit(130)`.
2. Wrap the wait + drain in a select-based driver:
   ```go
   waitDone := make(chan struct{})
   var outcomes map[string]string
   var waitErr error
   go func() {
       outcomes, waitErr = WaitForInstancesTerminal(ctx, c, instanceIDs, printer, 250*time.Millisecond)
       close(waitDone)
   }()

   var reason ShutdownReason
   select {
   case <-waitDone:
       // natural termination — classify via outcomes/waitErr
       reason = classifyOutcome(outcomes, waitErr)
   case sig := <-sigCh:
       logger.Info("signal received; draining", "signal", sig.String())
       reason = ReasonSignal
       cancelCtx()
       // second signal during drain: hard exit
       go func() {
           <-sigCh
           logger.Warn("second signal; hard exit")
           os.Exit(130)
       }()
   case <-time.After(flags.timeout): // only when flags.timeout > 0
       reason = ReasonTimeout
       cancelCtx()
   case fail := <-stack.FailCh():
       logger.Error("role failed", "role", fail.role, "err", fail.err)
       reason = ReasonAnyFailure
       cancelCtx()
   }
   ```
3. Account for `flags.timeout == 0` (no timeout): construct the time.After channel via a helper that returns a nil-receive (never fires).
4. Run `cd cmd && go build ./rimsky/...` and confirm clean.
5. Run `cd cmd && go test ./rimsky/cli/compose/... -count=1 -race`.
6. Run `make lint` from repo root.

---

## Pass 6: Acceptance — STORY-one-shot-to-terminal, STORY-audit-artifact, STORY-spawned-local-services, STORY-live-progress, STORY-script-friendly-outcome

**Goal:** Deliver each user-outcome story end-to-end through the real `rimsky` CLI binary against a sample manifest, and leave a proof artifact (demo or executable proof) behind for each. The five stories share the same booted stack and harness, so they batch into one acceptance pass per the brainstorm skill's "stories share acceptance passes by default" rule.
**Scope:** Tasks 22–28
**Falsifier (per story):**
- STORY-one-shot-to-terminal: the demo runs `rimsky compose run sample.yml` and the verb exits before all declared instances reach terminal, OR exits with no per-instance summary, OR returns non-zero on a manifest where every instance succeeds.
- STORY-audit-artifact: after the demo, `<.rimsky>/runs/<latest>/state.db` is missing or does not load in `sqlite3`, OR the recorded node-runs do not include the executed nodes by name, OR the run dir has no `blobs/` subdir.
- STORY-spawned-local-services: the executable proof spawns a stub executor via `--service`, the verb completes, but a `ps`-style check after exit finds the stub binary still alive, OR the manifest's nodes never reached the spawned binary (their dispatches failed or hung).
- STORY-live-progress: the live-progress demo captures output to a file as the run executes; the file has no lifecycle lines until after the run terminates, OR the timestamps of lifecycle lines all cluster at the end.
- STORY-script-friendly-outcome: the executable proof's three runs (success / failure / timeout) do not yield exit codes 0 / 1 / 2 respectively.

### Task 22: Build a stub executor binary for proof tests

**Files:** `cmd/rimsky/cli/compose/testdata/stub-executor/main.go` (new), `cmd/rimsky/cli/compose/testdata/stub-executor/README.md` (new)

**Background.** Many acceptance tests need a tiny gRPC executor that the spec's manifests can target. The existing `examples/executor/` is a reference implementation that's too heavy for unit/scenario tests. Build a minimal stub.

**Steps:**

1. The stub binary reads `RIMSKY_AGENT_PORT`, binds a gRPC server there, implements `proto:executor.proto::Executor.Execute` to immediately return success (or, when given a special attribute, return failure). This makes test manifests deterministic.
2. Write `testdata/stub-executor/main.go` mirroring `examples/executor/main.go`'s shape but simplified.
3. Build the binary at test time via a TestMain helper: `go build -o <tmpdir>/stub-executor ./testdata/stub-executor`.
4. `cmd/rimsky/cli/compose/testdata/stub-executor/README.md` documents the binary's purpose and how tests invoke it.

### Task 23: Build a sample compose manifest for the demo tests

**Files:** `cmd/rimsky/cli/compose/testdata/sample-manifest/rimsky-compose.yml` (new), `cmd/rimsky/cli/compose/testdata/sample-manifest/template-success.yml` (new), `cmd/rimsky/cli/compose/testdata/sample-manifest/template-failure.yml` (new)

**Background.** A two-instance manifest where one instance succeeds and one fails — drives the spec's STORY-one-shot-to-terminal Proof ("drive a two-instance manifest where one succeeds and one fails; the run completes on its own and reports both outcomes").

**Steps:**

1. Write `rimsky-compose.yml`:
   ```yaml
   project: sample-pipeline
   templates:
     - path: template-success.yml
       tag: success
     - path: template-failure.yml
       tag: failure
   instances:
     - template: success
       name: ok
     - template: failure
       name: oops
   executors:
     stub:
       transport: grpc
       endpoint: "127.0.0.1:9091"  # placeholder; --service overrides
   ```
2. Write minimal template YAMLs with a single node each, executor `stub`, parameters that the stub recognizes as "succeed" vs "fail".

### Task 24: Implement and deliver STORY-one-shot-to-terminal — demo + executable proof

**Files:** `examples/compose/one-shot-to-terminal-demo.sh` (new), `test/scenarios/compose_run_one_shot_terminal_test.go` (new)

**Story:** STORY-one-shot-to-terminal
**Proof form (from spec):** demo + executable proof — drive a two-instance manifest where one succeeds and one fails; the run completes on its own and reports both outcomes.

**Steps:**

1. Write `examples/compose/one-shot-to-terminal-demo.sh`:
   - Standard demo header (copyright, story-ref comment, falsifier-properties list).
   - Build the rimsky CLI binary if not already on PATH: `RIMSKY_BIN=${RIMSKY_BIN:-$(command -v rimsky)}; [ -z "$RIMSKY_BIN" ] && { echo "set RIMSKY_BIN or build with 'make cli'"; exit 1; }`.
   - Build the stub executor: `go build -o $(mktemp -u)/stub-executor ./cmd/rimsky/cli/compose/testdata/stub-executor`.
   - Copy the sample manifest to a tempdir.
   - Run: `"$RIMSKY_BIN" compose run --service stub=$STUB_BIN sample-manifest/rimsky-compose.yml; rc=$?`
   - Assert `rc == 1` (one instance failed → exit 1).
   - Grep output for per-instance summary lines for both `ok` and `oops` instances with their outcomes.
   - On any deviation, exit non-zero with diagnostic.
2. Write `test/scenarios/compose_run_one_shot_terminal_test.go`:
   - `TestComposeRunOneShotTerminal_E2E` — `t.TempDir()` for the cwd; spawn the rimsky CLI as a subprocess (built via `go build`); set up the sample manifest + stub executor; run; assert exit code is 1 (one failure); read `.rimsky/runs/<latest>/state.db` directly; assert two instance rows with outcomes `success` and `failed`.
3. Cite the story slug in the test file header comment: `// @story: one-shot-to-terminal`.
4. Run `bash examples/compose/one-shot-to-terminal-demo.sh` against a freshly built CLI. Confirm exit 0 (demo passes).
5. Run `go test ./test/scenarios/... -run ComposeRunOneShotTerminal -count=1` and confirm pass.

### Task 25: Deliver STORY-audit-artifact — demo

**Files:** `examples/compose/audit-artifact-demo.sh` (new)

**Story:** STORY-audit-artifact
**Proof form (from spec):** demo — drive a small failing manifest, then walk through opening the artifact and pulling the failing node-run's terminal event out by hand.

**Steps:**

1. Write `examples/compose/audit-artifact-demo.sh`:
   - Standard header + story reference.
   - Run `rimsky compose run` against a one-instance failure manifest.
   - After the verb exits, `LATEST=$(readlink .rimsky/latest)`.
   - Run `sqlite3 .rimsky/runs/$LATEST/state.db "SELECT id, node_id, state, error_class FROM rimsky_node_events WHERE state='failed' LIMIT 5;"`.
   - Assert the query returns at least one row with `state=failed`.
   - Print a summary that a human reader can follow: "opened the .db, pulled the failed node-run row, here it is".
2. Document the `sqlite3` query format in the script so a third-party reader sees exactly how to do the post-mortem.
3. Run the script against a built CLI. Confirm exit 0.

### Task 26: Deliver STORY-spawned-local-services — executable proof

**Files:** `test/scenarios/compose_run_spawned_services_test.go` (new)

**Story:** STORY-spawned-local-services
**Proof form (from spec):** executable proof — small manifest with one node referencing a stub local executor; the run launches the binary, drives the node through it to success, exits; a post-exit process check confirms no leak.

**Steps:**

1. `TestComposeRunSpawnedServices_NoLeakAfterExit`:
   - `t.TempDir()` for cwd.
   - Build the stub executor binary into the tempdir.
   - Build the rimsky CLI binary if needed.
   - Run the rimsky CLI as a subprocess with `--service stub=<path>`; manifest has one instance that calls stub. Pipe stderr to a buffer so the test can parse it after exit.
   - **PID capture**: parse stderr for the `spawned service` log event emitted by `spawnServices` in Task 19 (`"name": "stub", "pid": <N>, "port": <P>`). The stub-executor PID is the value of the `pid` field. The slog JSON handler in the verb emits one such line per spawn.
   - Wait for the subprocess to exit (success expected).
   - Then `proc, _ := os.FindProcess(stubPID); err := proc.Signal(syscall.Signal(0))`. The expected outcome: signal-0 returns a process-not-found error (the OS says the PID no longer maps to a live process).
2. Comment header: `// @story: spawned-local-services`. Plus `// @blessed-invariant: spawn-child-reaped-on-exit — verifies no leak after `rimsky compose run` returns`.
3. Run `go test ./test/scenarios/... -run ComposeRunSpawnedServices -count=1 -race` and confirm pass.

### Task 27: Deliver STORY-live-progress — demo

**Files:** `examples/compose/live-progress-demo.sh` (new)

**Story:** STORY-live-progress
**Proof form (from spec):** demo — record a transcript of a multi-instance manifest with one slow node; show the progress lines appearing as the node executes, not after it returns.

**Steps:**

1. Extend the stub executor (Task 22) with a `delay_ms` attribute that makes it `time.Sleep` for the configured duration before returning. The sample manifest variant for this demo has one slow instance (`delay_ms: 3000`) and one fast instance.
2. Write `examples/compose/live-progress-demo.sh`:
   - Run `rimsky compose run` with output piped to a script that prefixes each line with a timestamp: `ts=$(date +%s.%N); echo "$ts $line"`.
   - The transcript file accumulates timestamps.
   - After the verb exits, parse the transcript: assert that the line "instance ok: success" appears BEFORE "instance slow: success" (the fast instance terminal precedes the slow one). This proves progress lines are emitted live, not batched at the end.
3. Document the test in the script header. Exit 0 only on the live-ordering assertion.
4. Run `bash examples/compose/live-progress-demo.sh` and confirm exit 0.

### Task 28: Deliver STORY-script-friendly-outcome — executable proof

**Files:** `test/scenarios/compose_run_exit_codes_test.go` (new)

**Story:** STORY-script-friendly-outcome
**Proof form (from spec):** executable proof — three runs (clean success / one failed instance / a bounded run hitting its limit) verified to produce three distinct exit codes via a wrapper that exits 0, 1, 2 respectively.

**Steps:**

1. `TestComposeRunExitCodes_ThreeClasses`:
   - Subtest "success": all-success manifest; `rimsky compose run`; expect exit 0.
   - Subtest "failure": one-failed manifest; expect exit 1.
   - Subtest "timeout": manifest where the slow stub takes 10s; pass `--timeout 1s`; expect exit 2.
2. Each subtest uses `exec.Command` with the rimsky CLI subprocess; reads `cmd.ProcessState.ExitCode()`.
3. Comment header: `// @story: script-friendly-outcome`.
4. Run `go test ./test/scenarios/... -run ComposeRunExitCodes -count=1` and confirm all three subtests pass.

---

## Pass 7: Design-doc updates

**Goal:** Apply every bullet from the spec's `## Design changes` section to the design-doc catalog: mutate `concepts/rimsky.md`'s `## What it is` section, create the memory-blob-audit-gap tension, create five story files (one per locked story), create twenty-three decision files (one per TD).
**Scope:** Tasks 29–32
**Falsifier:** any spec-named design-doc artifact is missing from `.ok-planner/design/`; OR any new artifact body contains a file path, `code:` citation, or external-tool reference (violating the self-containment rule); OR any new artifact body contains forward-looking ("TODO", "deferred", "V2") or backward-looking ("previously called X", dated audit-trail) phrasing.

### Task 29: Mutate `concepts/rimsky.md`'s `## What it is` section

**Files:** `.ok-planner/design/concepts/rimsky.md` (mutate)

**Spec reference:** Design-changes bullet 1.

**Steps:**

1. Open `.ok-planner/design/concepts/rimsky.md`. Locate the `## What it is` section.
2. Replace the section's body verbatim with the spec's proposed text:
   > Operator-facing CLI for rimsky: a thin HTTP+JSON client over the control-api for operating a deployed rimsky stack, plus an embedded one-shot orchestration mode that self-hosts the runtime stack to drive a manifest to terminal without standing up rimsky infrastructure. The CLI is the binary operators invoke directly; the embedded stack reuses the same role implementations as the deployed binaries, configured for a single ephemeral run rooted at a per-run artifact directory.
3. Leave all other sections (Purpose, Boundaries, Invariants, Adjacent) unchanged.
4. Plumbline + ok-planner conventions: no `## Notes`, `## History`, dated entries, or "previously was X" lines anywhere in the file. The new prose stands on its own.

### Task 30: Create `tensions/memory-blob-audit-gap.md`

**Files:** `.ok-planner/design/tensions/memory-blob-audit-gap.md` (new)

**Spec reference:** Design-changes bullet 2.

**Steps:**

1. Create the file with this frontmatter and body:
   ```markdown
   ---
   tension: memory-blob-audit-gap
   status: open
   category: durability
   ---

   # memory-blob-audit-gap

   ## What is muddy

   The memory variant of `concept:blob-backend` stores blob bodies in an in-process map; the persisted event log and node-run rows reference those bodies by handle. When the unified process exits, the in-process map vanishes — but the persisted rows survive, referencing handles that no longer resolve. For long-running unified deployments using the memory blob backend, "blobs are ephemeral after process exit" is the documented and intended semantic. The muddiness is what that means for the audit trail: an operator (or post-mortem tool) reading the persisted event log encounters blob handles that resolve to nothing, with no in-band indicator distinguishing structural absence (memory blobs never persisted) from data loss (a backend that lost its data). A reader holding only the persisted event log cannot tell which case they are looking at.

   ## Evidence

   The memory backend is implemented at `code:lib/foundation/persistence/blob_memory.go` and gated to `env:RIMSKY_PROCESS_ROLE=unified` in `code:lib/foundation/persistence/blob_config.go`. Event-log writes reference blob handles uniformly across backends; no per-backend metadata flag indicates resolution-time absence semantics.

   ## Resolution candidates

   - Annotate memory-backend handles with a flag at write time so a reader knows they will not resolve after process exit, and surface that flag in event-log responses.
   - Restrict the memory backend further: legal only when no persisted audit consumer is configured (e.g., no lifecycle subscriber, no operator dashboard), so the gap cannot leak to a post-mortem reader.
   - Retire the memory backend; require unified-mode deployments to use inline or filesystem blobs.
   - Document the gap as a known characteristic of the memory backend and leave the resolution semantics unchanged.
   ```
2. The `## Evidence` section is allowed code citations per the tensions carve-out in the self-containment rule. The `## Resolution candidates` section is path-free (verified — no `code:` tags).

### Task 31: Create the five story files

**Files (new):**
- `.ok-planner/design/stories/one-shot-to-terminal.md`
- `.ok-planner/design/stories/audit-artifact.md`
- `.ok-planner/design/stories/spawned-local-services.md`
- `.ok-planner/design/stories/live-progress.md`
- `.ok-planner/design/stories/script-friendly-outcome.md`

**Spec reference:** Design-changes bullets 3–7.

**Steps for each file:**

1. Each file uses the existing story-file template (verified by reading `.ok-planner/design/stories/all-upstream-gating.md` and other existing stories — frontmatter has `story:` + `status: as-is`; sections are `## Role`, `## Capability`, `## Business value`, `## Acceptance`, `## Falsifier`, `## Proof`):
   ```markdown
   ---
   story: <slug>
   status: as-is
   ---

   # <human-readable title summarizing the capability>

   ## Role

   <role sentence — "As <role>, I can <capability>" style is fine, expanded to a full sentence>

   ## Capability

   <a paragraph describing what the user can now do and how it works at the visible level — the mechanism the user sees>

   ## Business value

   <a sentence on why this matters — the "so that …" rationale from the spec, expanded>

   ## Acceptance

   <Acceptance from spec, verbatim>

   ## Falsifier

   <Falsifier from spec, verbatim>

   ## Proof

   <Proof form + what must be exhibited, verbatim>
   ```
2. The Role / Capability / Business-value content for each of the five stories follows the spec's "As <role>, I can <capability>, so that <business value>" sentence — but each existing story expands these into fuller prose. Mirror that fullness: the Role section is a one-line sentence, the Capability section is a paragraph (1–3 sentences) describing what the user observes, and the Business value section is a sentence describing the rationale.
3. Self-containment audit per file: no file paths, no `code:` citations, no external-tool names. The five story bodies in the spec already satisfy this (the brainstorm review verified it).

### Task 32: Create the twenty-three decision files

**Files (new):**
- `.ok-planner/design/decisions/cli-verb.md`
- `.ok-planner/design/decisions/exposure-no-config.md`
- `.ok-planner/design/decisions/persistence-driver.md`
- `.ok-planner/design/decisions/blob-backend.md`
- `.ok-planner/design/decisions/artifact-layout.md`
- `.ok-planner/design/decisions/artifact-root-discovery.md`
- `.ok-planner/design/decisions/run-name.md`
- `.ok-planner/design/decisions/timestamp-format.md`
- `.ok-planner/design/decisions/launch-integration.md`
- `.ok-planner/design/decisions/launch-config-injection.md`
- `.ok-planner/design/decisions/migration-direct.md`
- `.ok-planner/design/decisions/network-binding.md`
- `.ok-planner/design/decisions/auth-anonymous-via-empty-key-ledger.md`
- `.ok-planner/design/decisions/compose-engine-reuse.md`
- `.ok-planner/design/decisions/termination.md`
- `.ok-planner/design/decisions/instance-self-termination.md`
- `.ok-planner/design/decisions/timeout-flag.md`
- `.ok-planner/design/decisions/exit-codes.md`
- `.ok-planner/design/decisions/progress-default.md`
- `.ok-planner/design/decisions/progress-flags.md`
- `.ok-planner/design/decisions/service-spawn-flag.md`
- `.ok-planner/design/decisions/services-source.md`
- `.ok-planner/design/decisions/graceful-shutdown.md`

**Spec reference:** Design-changes bullets 8–30.

**Steps for each file:**

1. Each file uses this template (substitute the TD-specific Choice/Rationale/Alternatives from the spec's `## Technical decisions` section verbatim):
   ```markdown
   ---
   decision: <slug>
   status: adopted
   ---

   # <slug>

   ## Choice

   <Choice from spec, verbatim>

   ## Rationale

   <Rationale from spec, verbatim>

   ## Alternatives

   <Alternatives from spec, verbatim; omit this section entirely if the spec's TD has no Alternatives field>
   ```
2. The spec's `## Technical decisions` section has been audited for self-containment in the brainstorm review round. Each TD's body text can be lifted verbatim.
3. Run `plumbline ./.ok-planner/` and confirm no violations (the design docs are not normally lint-scanned, but a quick check catches any path-form citations that snuck in).
4. After all files are created, regenerate `.ok-planner/design/concepts.md` — the TOC. The skill `discover-design` owns the TOC regeneration normally, but since this run touches only stories/decisions/tensions (not concepts other than the rimsky mutation), the TOC update is mechanical: re-render from the live `concepts/*.md` files. **Check whether the TOC is auto-regenerated**: `grep -l "Generated by\|do not edit" .ok-planner/design/concepts.md`. If it's hand-edited, skip; if auto, the implementer regenerates (the file's docs/comments say how).
5. The `.ok-planner/design/stories/` and `.ok-planner/design/decisions/` directories do not have a TOC (per the existing structure — verify by `ls .ok-planner/design/`).

---

## Manual checks after completion

(None — every story is delivered through an executable proof or a runnable demo script in Pass 6. There are no UI surfaces, no human-judgment evaluations, and no environment-specific behaviors that escape automation.)
