# `rimsky-cli` and `rimsky-compose` Implementation Plan

**Goal:** Ship the v1 `rimsky-cli` binary plus `rimsky-compose.yml` declarative manifest support, fully aligned with `docs/specs/2026-05-02-rimsky-cli-and-compose-design.md`.

**Architecture:** A single Go binary (`core/cmd/rimsky-cli/`) dispatching to per-verb-group handlers under `core/cli/`. Pure HTTP client over the existing control-api (`http://<endpoint>/templates`, `/tags`, `/instances`, `/events`, `/nodes`, `/admin/...`). Compose adds plan-and-apply orchestration over those calls, scoped to project-prefixed tags (`compose:<project>:<tag>`) and instance keys (`compose:<project>:<name>`). Reference assets for `init` are embedded via `//go:embed`.

**Tech Stack:** Go 1.22+ (matches existing module); stdlib `net/http` / `encoding/json` / `flag` / `embed` / `os/exec`; `gopkg.in/yaml.v3` (already present transitively via `core/config/`); chi only via re-using existing handlers — the CLI itself uses stdlib `flag` for subcommand dispatch. `httptest` from stdlib for the fake control-api fixture. No new dependencies expected; if any are added, document the reason.

---

## Pre-flight context for the implementer

Read these before starting:

- The spec: `docs/specs/2026-05-02-rimsky-cli-and-compose-design.md`. Authoritative on every behavioral question. When the plan and the spec differ, the spec wins; flag the discrepancy and continue.
- The control-api routes: `core/controlapi/{templates,tags,instances,nodes,events,health,admin_force_fire}.go`. Endpoint paths are bare (no `/v1/` prefix). The `idOrKey` chi parameter on `/instances/{idOrKey}` accepts UUID or `instance_key`.
- The instance termination model: `core/controlapi/instances.go` `handleDeleteInstance` (lines 262–331). `DELETE /instances/{id}` requires `terminated_at IS NOT NULL`; refused with HTTP 409 otherwise. There is no kill / abort path.
- The instance-create body shape: `core/controlapi/instances.go` `createInstanceRequest` struct.
- Tag conventions: `core/controlapi/tags.go`. `GET /tags` returns `{tags: [{tag, template_id, updated_at}], next_cursor}` and accepts `cursor`/`limit` only — **no prefix filter exists**; the CLI lists and filters client-side.
- Project rules: `.claude/rules/rules.md` and `.claude/rules/cold-read-cheatsheet.md`. Cold-read conventions apply to all new code.
- Build & test commands per `CLAUDE.md`: `go build ./... && go test ./... && make lint`.

The existing module is rooted at the repo root (`go.mod`); all new packages live under that module. The Go module path is `github.com/rimsky-ai/rimsky-core`.

---

## File structure

New files:

```
core/cmd/rimsky-cli/
  main.go                       # subcommand dispatcher
  version.go                    # build-stamped version
  embed.go                      # //go:embed directives for init assets

core/cli/
  client.go                     # HTTP client (one method per endpoint)
  client_errors.go              # control-api error parsing
  config.go                     # ~/.rimsky/config.yml load/save; context resolution
  endpoint.go                   # endpoint resolution (flag > env > config)
  output.go                     # human + JSON formatters; ANSI color
  flags.go                      # shared flags (-o, --endpoint, --yes, --no-color)
  templates.go                  # `template register/list/get/deploy/undeploy/rm`
  tags.go                       # `tag create/list/get/mv/rm`
  instances.go                  # `instance create/list/get/delete/nodes/events`
  nodes.go                      # `node get`
  admin.go                      # `admin force-fire/invalidate/reset`
  run.go                        # ergonomic top-level (run/register/deploy/...)
  init.go                       # `init` scaffold
  context.go                    # `ctx list/use/add/rm/current`
  health.go                     # `health`

core/cli/compose/
  manifest.go                   # YAML schema + local validation
  resolver.go                   # template-spec → hash; tag prefixing
  state.go                      # query control-api for compose-owned resources
  plan.go                       # diff manifest vs state → ordered plan
  apply.go                      # execute plan steps 1–8 serially
  down.go                       # compose down sequencing
  dev.go                        # dev up/down/status; infra hook execution
  cmd.go                        # compose up/down/plan/status dispatchers

core/cli/clitest/
  server.go                     # httptest-backed fake control-api
  state.go                      # in-memory state (templates/tags/instances/lifecycle)
  manifest.go                   # builders for compose manifests in tests

core/cli/embedded/
  rimsky-compose.yml.tmpl       # init scaffold template
  deploy/docker-compose.yml     # copied at build time from /deploy
  deploy/store-filesystem.yml   # copied at build time
  deploy/supervisor-config.yml  # copied at build time
  graphs/example.yml            # minimal HTTP-stub graph

Dockerfile.cli                  # distroless-based CLI image

test/smoke/cli/
  smoke_test.go                 # E2E against deploy/docker-compose.yml
```

Changed files:

```
Makefile                        # add `cli`, `cli-release`, `cli-image` targets
go.mod                          # verify yaml.v3 present
docs/operator-guide.md          # add CLI / compose sections
docs/architecture.md            # mention core/cmd/rimsky-cli + core/cli
docs/glossary.md                # add compose project / manifest / context / infra
CLAUDE.md                       # add CLI gotchas
CHANGELOG.md                    # Unreleased bullet
```

The `core/cli/embedded/deploy/*` files are copies of the live `deploy/*` files. They are kept in sync **manually**; a Makefile target `cli-sync-embedded` copies them. Drift between `deploy/` and `core/cli/embedded/deploy/` is acceptable (the embedded versions are pinned to the CLI's build time per the spec) but the sync target makes refresh easy.

---

## Tasks

### Task 1 — Skeleton and version stamp

**Goal:** Lay down the binary entry point with no real verbs; prove the build pipeline produces a runnable binary.

**Files:** `core/cmd/rimsky-cli/main.go`, `core/cmd/rimsky-cli/version.go`, `Makefile`.

1. Create `core/cmd/rimsky-cli/version.go`:

   ```go
   // version.go — build-stamped version string.
   //
   // Set at build time via -ldflags "-X main.version=<version>".
   package main

   var version = "dev"
   ```

2. Create `core/cmd/rimsky-cli/main.go` with a minimal dispatcher that handles `version` and `--help`:

   ```go
   // main.go — rimsky-cli entry point. Dispatches subcommands to handlers
   // in core/cli/. Hand-rolled subcommand routing on os.Args[1].
   package main

   import (
       "fmt"
       "os"
   )

   func main() {
       if len(os.Args) < 2 {
           printRootUsage(os.Stderr)
           os.Exit(2)
       }
       switch os.Args[1] {
       case "version", "--version", "-v":
           fmt.Printf("rimsky-cli %s\n", version)
           return
       case "help", "--help", "-h":
           printRootUsage(os.Stdout)
           return
       default:
           fmt.Fprintf(os.Stderr, "rimsky-cli: unknown command %q\n\n", os.Args[1])
           printRootUsage(os.Stderr)
           os.Exit(2)
       }
   }

   func printRootUsage(w *os.File) {
       fmt.Fprintln(w, "rimsky-cli — orchestration CLI for the rimsky platform.")
       fmt.Fprintln(w, "usage: rimsky-cli <command> [args]")
       fmt.Fprintln(w, "")
       fmt.Fprintln(w, "(verbs land in subsequent tasks)")
   }
   ```

3. Add a `Makefile` target. Open `Makefile`, find the `build` section, and append:

   ```makefile
   .PHONY: cli
   cli:
   	go build -ldflags "-X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)" -o bin/rimsky-cli ./core/cmd/rimsky-cli/

   .PHONY: cli-release
   cli-release:
   	@mkdir -p bin/release
   	@for os in linux darwin; do \
   	  for arch in amd64 arm64; do \
   	    GOOS=$$os GOARCH=$$arch go build -ldflags "-X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)" -o bin/release/rimsky-cli_$${os}_$${arch} ./core/cmd/rimsky-cli/; \
   	  done; \
   	done; \
   	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)" -o bin/release/rimsky-cli_windows_amd64.exe ./core/cmd/rimsky-cli/
   ```

4. **Verify:** `go build ./core/cmd/rimsky-cli/ && go vet ./core/cmd/rimsky-cli/` exits 0. Then `make cli && ./bin/rimsky-cli version` prints something matching `^rimsky-cli `. Then `./bin/rimsky-cli foo; echo $?` prints `2`.

---

### Task 2 — HTTP client foundation

**Goal:** A typed HTTP client with one method per control-api endpoint. Pure pass-through; no business logic.

**Files:** `core/cli/client.go`, `core/cli/client_errors.go`, `core/cli/client_test.go`.

1. Create `core/cli/client.go` with a `Client` struct holding `endpoint string`, `httpClient *http.Client`, `userAgent string`. Constructor `NewClient(endpoint string) *Client`. The HTTP client uses `http.DefaultTransport` plus a 30s default timeout (overridable via `--timeout` later; not in v1 plan).

2. Add request methods, one per endpoint. Use stdlib only. **Note:** there is no `GetTag` method; the control-api does not expose a per-tag GET endpoint. `tag get` (Task 8) does a list-and-filter against `GET /tags`. Similarly, `GET /tags`, `GET /instances`, and `GET /templates` accept only pagination filters server-side; CLI flags like `--tag-prefix`, `--key-prefix`, `--state` (Tasks 7/8/9) implement client-side filtering after listing.

   Helper:

   ```go
   // do executes req and decodes the JSON body into out (which may be nil for
   // 204 responses or when the caller does not care). Non-2xx responses are
   // returned as *APIError carrying the status code and the decoded body.
   func (c *Client) do(req *http.Request, out any) error { ... }
   ```

3. Add the following methods (each one issues an HTTP call against the bare path; no `/v1/` prefix):

   ```go
   // Templates
   func (c *Client) RegisterTemplate(ctx context.Context, body RegisterTemplateRequest) (*Template, error)
   func (c *Client) ListTemplates(ctx context.Context, q ListTemplatesQuery) (*ListTemplatesResponse, error)
   func (c *Client) GetTemplate(ctx context.Context, ref string) (*Template, error)
   func (c *Client) DeployTemplate(ctx context.Context, ref string) (*Template, error)
   func (c *Client) UndeployTemplate(ctx context.Context, ref string) (*Template, error)
   func (c *Client) DeleteTemplate(ctx context.Context, ref string) error

   // Tags
   func (c *Client) CreateTag(ctx context.Context, body CreateTagRequest) (*Tag, error)
   func (c *Client) ListTags(ctx context.Context, q ListTagsQuery) (*ListTagsResponse, error)
   func (c *Client) MoveTag(ctx context.Context, tag string, body MoveTagRequest) (*Tag, error)
   func (c *Client) DeleteTag(ctx context.Context, tag string) error

   // Instances
   func (c *Client) CreateInstance(ctx context.Context, body CreateInstanceRequest) (*Instance, error)
   func (c *Client) ListInstances(ctx context.Context, q ListInstancesQuery) (*ListInstancesResponse, error)
   func (c *Client) GetInstance(ctx context.Context, idOrKey string) (*Instance, error)
   func (c *Client) DeleteInstance(ctx context.Context, idOrKey string) error
   func (c *Client) ListInstanceNodes(ctx context.Context, idOrKey string) (*ListInstanceNodesResponse, error)

   // Nodes
   func (c *Client) GetNode(ctx context.Context, id string) (*Node, error)
   func (c *Client) InvalidateNode(ctx context.Context, id string, body InvalidateNodeRequest) error
   func (c *Client) ResetNode(ctx context.Context, id string) error

   // Events
   func (c *Client) ListEvents(ctx context.Context, q ListEventsQuery) (*ListEventsResponse, error)

   // Admin
   func (c *Client) AdminForceFire(ctx context.Context, nodeID string) error

   // Health
   func (c *Client) Health(ctx context.Context) (*HealthResponse, error)
   ```

   Define request/response structs in this same file. Field names match the JSON shape returned by the control-api handlers. Read the existing handlers under `core/controlapi/` to extract the exact shapes — do not invent fields.

4. Create `core/cli/client_errors.go`:

   ```go
   // client_errors.go — APIError carries non-2xx control-api responses.
   package cli

   type APIError struct {
       Status int
       URL    string
       Method string
       Body   map[string]any // decoded JSON body, may be nil
   }

   func (e *APIError) Error() string { ... }

   func IsConflict(err error) bool   { ... } // 409
   func IsNotFound(err error) bool   { ... } // 404
   func IsBadRequest(err error) bool { ... } // 400
   ```

5. Create `core/cli/client_test.go` with table-driven tests using `httptest.NewServer`:

   - One subtest per method.
   - Each subtest stands up a server, asserts the request method+path+body, returns a canned response, and asserts the parsed Go value.
   - Failure cases: 404 → `IsNotFound`; 409 → `IsConflict`; 5xx → generic `*APIError`.

6. **Verify:**
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/cli/... -run TestClient -count=1 -race` passes.
   - `go vet ./core/cli/...` exits 0.

---

### Task 3 — Config file and endpoint resolution

**Goal:** Load and save `~/.rimsky/config.yml`. Resolve the active endpoint from flag > env > config.

**Files:** `core/cli/config.go`, `core/cli/endpoint.go`, `core/cli/config_test.go`, `core/cli/endpoint_test.go`.

1. Create `core/cli/config.go`:

   ```go
   // config.go — ~/.rimsky/config.yml load/save.
   //
   // File format (per spec §4.2):
   //   current_context: dev
   //   contexts:
   //     dev: { endpoint: http://localhost:8080 }
   //     staging: { endpoint: https://rimsky.staging.example.com }
   package cli

   type Config struct {
       CurrentContext string             `yaml:"current_context,omitempty"`
       Contexts       map[string]Context `yaml:"contexts,omitempty"`
   }

   type Context struct {
       Endpoint string `yaml:"endpoint"`
   }

   func DefaultConfigPath() (string, error) { ... } // ~/.rimsky/config.yml
   func LoadConfig(path string) (*Config, error)   // returns &Config{} if file missing
   func SaveConfig(path string, cfg *Config) error // creates ~/.rimsky/ if missing
   ```

2. Validate context names against `^[a-zA-Z][a-zA-Z0-9._-]{0,62}$` in `SaveConfig` and in any `Set` operations.

3. Create `core/cli/endpoint.go`:

   ```go
   // endpoint.go — endpoint resolution. Precedence: flag > env > config.
   package cli

   // ResolveEndpoint returns the API endpoint URL to use.
   // - flag is the value of --endpoint (empty if unset).
   // - env reads RIMSKY_CONTROL_API; if set, used when flag is empty.
   // - cfgPath is the path to the config file (typically DefaultConfigPath()).
   // - manifestContext is from rimsky-compose.yml's `context:` field, if any.
   //
   // Returns the resolved URL or an error if no source supplied one.
   func ResolveEndpoint(flag, env string, cfgPath string, manifestContext string) (string, error) { ... }
   ```

   Precedence rules (per spec §4.1 + §2.3):
   - If `flag != ""`, return it.
   - If `manifestContext != ""`, look it up in the config; error if missing.
   - If `env != ""`, return it.
   - Otherwise, use the config's `current_context`. If unset, error: "no endpoint configured; set --endpoint, RIMSKY_CONTROL_API, or run `rimsky-cli ctx use <name>`."

4. Tests: in `core/cli/config_test.go` round-trip a config file (load nonexistent → empty; save → load → equal); in `core/cli/endpoint_test.go` cover every precedence permutation including the manifest-pin case and the unknown-context error.

5. **Verify:** `go test ./core/cli/... -run "TestConfig|TestEndpoint" -count=1` passes.

---

### Task 4 — Output formatters and shared flags

**Goal:** Human-readable table output and JSON output for representative response shapes; shared `-o`, `--endpoint`, `--yes`, `--no-color` flags.

**Files:** `core/cli/output.go`, `core/cli/flags.go`, `core/cli/output_test.go`.

1. Create `core/cli/output.go`:

   ```go
   // output.go — human (table/text) and JSON formatters.
   //
   // ANSI color is on when stdout is a TTY and --no-color is not set.
   package cli

   type Format int

   const (
       FormatHuman Format = iota
       FormatJSON
   )

   func ParseFormat(s string) (Format, error)

   // EmitJSON writes a value as indented JSON.
   func EmitJSON(w io.Writer, v any) error

   // EmitTable writes a header row and data rows separated by tabs;
   // intended for `template list`, `instance list`, etc.
   func EmitTable(w io.Writer, headers []string, rows [][]string)

   // EmitKV writes key:value lines for single-record output (e.g. `health`).
   func EmitKV(w io.Writer, pairs [][2]string)

   // ColorEnabled returns true if color codes should be used.
   func ColorEnabled(w io.Writer, noColorFlag bool) bool
   ```

2. Create `core/cli/flags.go` with helpers for the shared flags:

   ```go
   // flags.go — shared flag definitions.
   package cli

   type CommonFlags struct {
       Endpoint string
       Format   Format
       Yes      bool
       NoColor  bool
   }

   // RegisterCommonFlags wires the common flags onto fs. Call from each
   // subcommand's flag.NewFlagSet.
   func RegisterCommonFlags(fs *flag.FlagSet, out *CommonFlags) { ... }
   ```

3. Tests: format string parsing; table emission with empty rows; JSON output stability; color detection on TTY vs pipe (mock with `os.Pipe`).

4. **Verify:** `go test ./core/cli/... -run "TestEmit|TestParseFormat|TestColor" -count=1` passes.

---

### Task 5 — Health verb (proves wire-up end-to-end)

**Goal:** First end-to-end verb, smallest possible. Shake out the dispatcher → flags → endpoint → client → output pipeline.

**Files:** `core/cli/health.go`, `core/cmd/rimsky-cli/main.go`, `core/cli/health_test.go`.

1. Create `core/cli/health.go`:

   ```go
   // health.go — `rimsky-cli health`. Prints the control-api's /health response.
   package cli

   func RunHealth(ctx context.Context, args []string) int {
       fs := flag.NewFlagSet("health", flag.ExitOnError)
       var common CommonFlags
       RegisterCommonFlags(fs, &common)
       _ = fs.Parse(args)

       endpoint, err := ResolveEndpoint(common.Endpoint, os.Getenv("RIMSKY_CONTROL_API"), defaultCfgPath(), "")
       if err != nil { ... return 2 }

       client := NewClient(endpoint)
       resp, err := client.Health(ctx)
       if err != nil { ... return 1 }

       switch common.Format {
       case FormatJSON:
           EmitJSON(os.Stdout, resp)
       default:
           EmitKV(os.Stdout, [][2]string{{"status", resp.Status}, {"endpoint", endpoint}})
       }
       return 0
   }
   ```

2. Wire it up in `core/cmd/rimsky-cli/main.go`. Replace the placeholder `default:` with a switch table that imports and dispatches to the `cli` package:

   ```go
   import "github.com/rimsky-ai/rimsky-core/core/cli"

   ...
   case "health":
       os.Exit(cli.RunHealth(context.Background(), os.Args[2:]))
   ```

3. Tests: `core/cli/health_test.go` uses `httptest.NewServer` to fake the control-api's `/health` and asserts both human and JSON output. Set `RIMSKY_CONTROL_API` via `t.Setenv`.

4. **Verify:**
   - `go test ./core/cli/... -run TestHealth -count=1` passes.
   - `make cli && RIMSKY_CONTROL_API=http://localhost:1 ./bin/rimsky-cli health; echo $?` prints `1` (no server listening).

---

### Task 6 — Fake control-api fixture (clitest)

**Goal:** A reusable in-memory fake for the rest of the CLI tests. All endpoints the CLI uses, plus configurable failure injection.

**Files:** `core/cli/clitest/server.go`, `core/cli/clitest/state.go`, `core/cli/clitest/server_test.go`.

1. Create `core/cli/clitest/state.go` with an `InMemoryState` struct holding maps for templates, tags, instances, lifecycle rows, events. Methods: `RegisterTemplate`, `CreateTag`, `MoveTag`, `DeleteTag`, `DeleteTemplate`, `DeployTemplate`, `UndeployTemplate`, `CreateInstance`, `ListInstances`, `GetInstance`, `DeleteInstance`, etc. The state mirrors the control-api's contracts:

   - Tag delete on a hash with no other tags allows template delete.
   - Template delete refused when state is `deployed`.
   - Template delete refused when any non-terminal instance binds.
   - Instance delete refused when `terminated_at IS NULL`.
   - Instance create with same `instance_key` against same `template_hash` collides on the unique key.
   - Tag create with hash-shape input is rejected (regex per spec §1.1).

2. Create `core/cli/clitest/server.go`:

   ```go
   // server.go — httptest-backed fake control-api.
   //
   // Usage:
   //   srv := clitest.NewServer(t)
   //   defer srv.Close()
   //   srv.State.RegisterTemplate(...)
   //   client := cli.NewClient(srv.URL)
   package clitest

   type Server struct {
       *httptest.Server
       State *InMemoryState
       // Hooks: when set, the corresponding handler returns the supplied
       // status code and body before touching state. Lets tests inject
       // 5xx and validate retry / failure paths.
       FailNext map[string]FailureSpec // keyed by "METHOD path"
   }

   type FailureSpec struct {
       Status int
       Body   any
       // Times: how many subsequent calls fail; 0 means once.
       Times int
   }

   func NewServer(t testing.TB) *Server { ... }
   ```

3. The handler routes match every endpoint the CLI uses. Use chi for ergonomic route matching (the project already depends on chi).

4. Tests in `core/cli/clitest/server_test.go`: round-trip every endpoint; failure injection works; state mutations persist across calls.

5. **Verify:** `go test ./core/cli/clitest/... -count=1 -race` passes.

---

### Task 7 — `template` subgroup verbs

**Goal:** Implement and test all six `template` verbs.

**Files:** `core/cli/templates.go`, `core/cli/templates_test.go`, `core/cmd/rimsky-cli/main.go` (add dispatch).

1. Implement `core/cli/templates.go` with handlers `RunTemplateRegister`, `RunTemplateList`, `RunTemplateGet`, `RunTemplateDeploy`, `RunTemplateUndeploy`, `RunTemplateRm`. Each:

   - Owns a `flag.FlagSet`.
   - Resolves endpoint via `ResolveEndpoint`.
   - Calls the corresponding `Client` method.
   - Emits human or JSON output.
   - Returns exit codes per spec §5.3 (0 success, 1 runtime, 2 usage).

2. `RunTemplateRegister` reads the spec file from `<file>` (positional arg). The file is YAML — convert to JSON via `gopkg.in/yaml.v3` → `interface{}` → `json.Marshal`. The control-api accepts JSON. Validate that the user-supplied `--tag`, if present, does not start with `compose:` (reject with exit 2 and a clear error per spec §8.3).

3. Add a sub-dispatcher in `main.go`:

   ```go
   case "template":
       if len(os.Args) < 3 {
           fmt.Fprintln(os.Stderr, "usage: rimsky-cli template <register|list|get|deploy|undeploy|rm> ...")
           os.Exit(2)
       }
       sub := os.Args[2]
       rest := os.Args[3:]
       switch sub {
       case "register":   os.Exit(cli.RunTemplateRegister(context.Background(), rest))
       ...
       }
   ```

4. Tests in `core/cli/templates_test.go` use `clitest.NewServer` and assert:
   - Register with valid spec → 201, human output prints hash.
   - Register with `--tag compose:foo:bar` → exit 2 (reserved-prefix rejection); no API call made.
   - List → table output sorted by tag.
   - Get nonexistent → exit 1 with "not found" output.
   - Deploy already-deployed → 200, human output says "already deployed."
   - Rm with active instances → 409, exit 1.

5. **Verify:** `go test ./core/cli/... -run TestTemplate -count=1 -race` passes.

---

### Task 8 — `tag` subgroup verbs

**Goal:** Implement and test all five `tag` verbs.

**Files:** `core/cli/tags.go`, `core/cli/tags_test.go`, `core/cmd/rimsky-cli/main.go`.

1. Implement `RunTagCreate`, `RunTagList`, `RunTagGet`, `RunTagMv`, `RunTagRm`. `RunTagGet` does a list-and-filter against `GET /tags` since no per-tag GET exists (per spec §1.6).

2. Reject `--tag compose:*` in `RunTagCreate` (same rationale as templates).

3. Wire dispatch in `main.go`.

4. Tests cover happy path + reserved-prefix rejection + "tag not found" via list-fallback.

5. **Verify:** `go test ./core/cli/... -run TestTag -count=1 -race` passes.

---

### Task 9 — `instance` and `node` subgroups

**Goal:** All `instance` verbs (including the polling logic for `events --follow`) plus `node get`.

**Files:** `core/cli/instances.go`, `core/cli/nodes.go`, `core/cli/instances_test.go`, `core/cli/nodes_test.go`, `core/cmd/rimsky-cli/main.go`.

1. Implement `RunInstanceCreate`, `RunInstanceList`, `RunInstanceGet`, `RunInstanceDelete`, `RunInstanceNodes`, `RunInstanceEvents`. Implement `RunNodeGet`.

2. `RunInstanceDelete` propagates 409 → exit 1 with the control-api's error message (per spec §1.3 `rm-instance`).

3. `RunInstanceEvents` with `--follow`:
   - If `<id-or-key>` looks like a UUID, use directly. Otherwise call `GET /instances/{key}` first to resolve the UUID.
   - Loop: `GET /events?instance_id=<UUID>&cursor=<last>&limit=100`. Print each event. If response has `next_cursor`, advance and continue without sleeping. If empty, sleep `--poll-interval` (default 1s) and retry.
   - SIGINT (signal.Notify on os.Interrupt) cleanly exits 0.

4. Wire dispatch.

5. Tests cover happy path; 409 on non-terminal delete; `--follow` polls and prints multiple batches; key→UUID resolution.

6. **Verify:** `go test ./core/cli/... -run "TestInstance|TestNode" -count=1 -race` passes.

---

### Task 10 — `admin` subgroup

**Goal:** `admin force-fire`, `admin invalidate`, `admin reset`.

**Files:** `core/cli/admin.go`, `core/cli/admin_test.go`, `core/cmd/rimsky-cli/main.go`.

1. Implement the three handlers. `admin invalidate` accepts `--reason <text>` and posts `{reason: text}` (matching the control-api's `invalidateNodeRequest`).

2. Wire dispatch.

3. Tests with `clitest.NewServer`.

4. **Verify:** `go test ./core/cli/... -run TestAdmin -count=1 -race` passes.

---

### Task 11 — `ctx` subgroup

**Goal:** Context CRUD over `~/.rimsky/config.yml`.

**Files:** `core/cli/context.go`, `core/cli/context_test.go`, `core/cmd/rimsky-cli/main.go`.

1. Implement `RunCtxList`, `RunCtxUse`, `RunCtxAdd`, `RunCtxRm`, `RunCtxCurrent`. Each loads the config via `LoadConfig(DefaultConfigPath())`, mutates if needed, and `SaveConfig`s.

2. Validation:
   - `ctx add <name>` refused (exit 2) if `<name>` already exists.
   - `ctx rm <name>` refused (exit 2) if it's the current context (must `ctx use` something else first).
   - Context names must match the regex `^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`.

3. Tests use `t.TempDir` for config-file isolation; pass the temp path as a function-level parameter (`RunCtxList` etc. accept a config-path argument so tests can override) — design the CLI handlers with optional path injection so they're testable. The `main.go` dispatcher passes `DefaultConfigPath()`.

4. Wire dispatch.

5. **Verify:** `go test ./core/cli/... -run TestCtx -count=1 -race` passes.

---

### Task 12 — Ergonomic top-level verbs

**Goal:** `run`, `register`, `deploy`, `undeploy`, `instantiate`, `rm-instance`, `ls`, `logs`. Aliases for the literal verbs except `run` (which composes three calls).

**Files:** `core/cli/run.go`, `core/cli/run_test.go`, `core/cmd/rimsky-cli/main.go`.

1. Aliases (`register`, `deploy`, etc.) are thin wrappers that call the corresponding `RunTemplateXxx` / `RunInstanceXxx` function.

2. `RunRun` (the `run` verb): registers, deploys, instantiates, prints `instance_id`. With `--no-keep`: polls `GetInstance` until `terminated_at IS NOT NULL` (interval `--poll-interval`, default 1s; `--timeout`, default unbounded), then deletes the instance, undeploys + deletes the template (warn-on-409 instead of erroring out per spec §1.3).

3. `RunLs` defaults to `instance list`; accepts `templates`, `instances`, `tags` as the first positional.

4. `RunLogs` is `instance events --follow` with the same `<id-or-key>` resolution.

5. Tests:
   - `run` happy path with `--keep`: three API calls in order.
   - `run --no-keep`: poll, then delete, then undeploy, then delete-template.
   - `run --no-keep` when delete-template returns 409 (other tags still exist): warning printed, exit 0.
   - `ls` defaults to instances.

6. Wire dispatch.

7. **Verify:** `go test ./core/cli/... -run "TestRun|TestLs|TestLogs" -count=1 -race` passes.

---

### Task 13 — Embedded assets

**Goal:** Embed the reference docker-compose, store config, supervisor config, and example graph; `init` will use them.

**Files:** `core/cli/embedded/` (directory), `core/cmd/rimsky-cli/embed.go`, `core/cli/embedded/embedded.go`, `core/cli/embedded/embedded_test.go`, `Makefile`.

1. Create `core/cli/embedded/` with these files:
   - `deploy/docker-compose.yml` — copy of the live `deploy/docker-compose.yml`.
   - `deploy/store-filesystem.yml` — copy.
   - `deploy/supervisor-config.yml` — copy.
   - `graphs/example.yml` — a minimal one-node template using `http-node` executor in stub mode. **Canonical source for the field shape:** `core/node/template.go` defines `TemplateSpec`, `TemplateNodeDef`, `NodeAttributesDef`. Reference fixture: `test/smoke/fixtures/template.yml`. Key gotchas verified against the actual struct:
     - Top-level fields: `name`, `version`, `frame_resolution`, `nodes`, `params_schema`. `name` and `version` are required.
     - Each node uses `type:` (NOT `name:`) — `TemplateNodeDef.Type` is the field.
     - `attributes:` block uses `schema:` (NOT `properties:`) — `NodeAttributesDef.Schema` is a JSON-Schema map.
     - `stores`, `locks`, `inherits`, `dependencies` are all optional and may be omitted entirely (no need to write empty arrays).

     Use this shape:

     ```yaml
     name: example
     version: "1.0"
     frame_resolution: coalesce
     params_schema:
       type: object
       additionalProperties: true
     nodes:
       - type: hello
         executor: http-node
         userdata:
           url: "http://example.invalid/hello"
           method: GET
         attributes:
           schema:
             type: object
             additionalProperties: true
     ```

     The example must round-trip through `core/node.ValidateTemplate` cleanly. Verification step (below) catches any shape error.
   - `rimsky-compose.yml.tmpl` — template-string YAML with `{{.Project}}` placeholder. Concrete:

     ```yaml
     project: {{.Project}}

     infra:
       up:
         command: ["docker", "compose", "-f", "deploy/docker-compose.yml", "up", "-d"]
         wait_for: "http://localhost:8080/health"
         timeout: 60s
       down:
         command: ["docker", "compose", "-f", "deploy/docker-compose.yml", "down", "-v"]

     rimsky_config:
       inline:
         stores:
           content:
             endpoint: "grpc://store-filesystem:9100"
             capabilities:
               write_semantics: direct
         named_locks: {}
         executors:
           http-node:
             transport: grpc
             endpoint: "http-node:9091"
             tls: off

     templates:
       - path: ./graphs/example.yml
         tag: example@1.0
         state: deployed

     instances:
       - template: example@1.0
         name: hello
         params: {}
         restart: never
     ```

     **Note on the template's tag:** the manifest declares the bare tag `example@1.0`; compose registers it under the project-prefixed form `compose:{{.Project}}:example@1.0` (per spec §2.6). The instance's `template:` field accepts the bare form and the resolver prefixes it.

2. Create `core/cli/embedded/embedded.go`:

   ```go
   // embedded.go — //go:embed boundary for init scaffold assets.
   package embedded

   import "embed"

   //go:embed deploy graphs rimsky-compose.yml.tmpl
   var FS embed.FS
   ```

3. Add a Makefile target to refresh the deploy/* embedded copies from the live `deploy/`:

   ```makefile
   .PHONY: cli-sync-embedded
   cli-sync-embedded:
   	cp deploy/docker-compose.yml core/cli/embedded/deploy/docker-compose.yml
   	cp deploy/store-filesystem.yml core/cli/embedded/deploy/store-filesystem.yml
   	cp deploy/supervisor-config.yml core/cli/embedded/deploy/supervisor-config.yml
   ```

4. Tests in `core/cli/embedded/embedded_test.go` assert:
   - Each expected path is readable from the embed.FS.
   - `rimsky-compose.yml.tmpl` parses as a Go `text/template`.
   - **`graphs/example.yml` round-trips through `core/node.ValidateTemplate` cleanly.** This catches the field-shape risk noted in step 1. Use the existing test helpers in `core/node/` for validation; reference how scenario tests load their template fixtures.

5. **Verify:** `go test ./core/cli/embedded/... -count=1` passes. `go build ./core/cmd/rimsky-cli/` succeeds.

---

### Task 14 — `init` verb

**Goal:** Scaffold a starter project from embedded assets.

**Files:** `core/cli/init.go`, `core/cli/init_test.go`, `core/cmd/rimsky-cli/main.go`.

1. Implement `RunInit`:
   - Resolve target directory: positional arg, defaulting to `.`.
   - Determine project name: directory basename, sanitized to match `^[a-z][a-z0-9-]{0,62}$` (lowercase, replace non-conforming chars with `-`).
   - For each path in the embedded FS:
     - Compute target path: `target/<embedded path>` (with `rimsky-compose.yml.tmpl` rendered to `rimsky-compose.yml`).
     - If the file exists and `--force` is not set, exit 2 with "file already exists; pass --force to overwrite."
     - Render the rimsky-compose template using `text/template` against `{Project: <name>}`.
     - Write each file with `0644` (or `0755` for any directory).
   - Create `.rimsky/` empty directory.
   - Create or append-update `.gitignore`: ensure `/.rimsky/` is on a line by itself.
   - Print "scaffolded rimsky project at <target>" plus a list of created paths.

2. Tests in `core/cli/init_test.go`:
   - `t.TempDir`, run `RunInit`, walk the directory and assert each scaffold file is present and parseable.
   - Re-run without `--force` → exit 2.
   - Re-run with `--force` → succeeds.
   - Project-name sanitization (e.g., a directory `My Project` becomes `my-project`).

3. Wire dispatch.

4. **Verify:** `go test ./core/cli/... -run TestInit -count=1` passes.

---

### Task 15 — Compose manifest parsing and validation

**Goal:** Load `rimsky-compose.yml`; enforce every validation rule from spec §2.8.

**Files:** `core/cli/compose/manifest.go`, `core/cli/compose/manifest_test.go`.

1. Define the manifest types in `core/cli/compose/manifest.go`:

   ```go
   type Manifest struct {
       Project      string         `yaml:"project"`
       Context      string         `yaml:"context,omitempty"`
       Infra        *Infra         `yaml:"infra,omitempty"`
       RimskyConfig *RimskyConfig  `yaml:"rimsky_config,omitempty"`
       Templates    []TemplateRef  `yaml:"templates"`
       Instances    []InstanceRef  `yaml:"instances"`
   }

   type Infra struct {
       Up   *InfraCommand `yaml:"up,omitempty"`
       Down *InfraCommand `yaml:"down,omitempty"`
   }

   type InfraCommand struct {
       Command []string `yaml:"command"`
       WaitFor string   `yaml:"wait_for,omitempty"`
       Timeout string   `yaml:"timeout,omitempty"` // parsed as time.Duration
   }

   type RimskyConfig struct {
       Inline map[string]any `yaml:"inline,omitempty"`
       Path   string         `yaml:"path,omitempty"`
   }

   type TemplateRef struct {
       Path  string `yaml:"path"`
       Tag   string `yaml:"tag"`
       State string `yaml:"state,omitempty"` // default: "deployed"
   }

   type InstanceRef struct {
       Template string         `yaml:"template"`
       Name     string         `yaml:"name"`
       Params   map[string]any `yaml:"params,omitempty"`
       Restart  string         `yaml:"restart,omitempty"` // default: "never"
   }
   ```

2. Implement `LoadManifest(path string) (*Manifest, error)`. Parses YAML; runs `Validate()`. Returns a multi-error containing every failure (use `errors.Join`).

3. Implement `Validate()`:

   ```
   - project required; matches ^[a-z][a-z0-9-]{0,62}$
   - context optional; matches ^[a-zA-Z][a-zA-Z0-9._-]{0,62}$
   - infra.up.wait_for if set: parses as URL with scheme http or https
   - infra.up.timeout if set: parses as time.Duration
   - rimsky_config.inline and rimsky_config.path mutually exclusive
   - templates[].path required; tag required, matches the control-api tag regex
     ^[a-zA-Z][a-zA-Z0-9._:@/-]{0,254}$, must NOT start with "compose:",
     must NOT match the hash regex ^sha256-[0-9a-f]{64}$
   - templates[].state defaults to "deployed"; must be in {registered, deployed}
   - instances[].name required; matches ^[a-z][a-z0-9-]{0,62}$
   - instances[].template required; resolves to either a manifest tag or a hash
   - instances[].restart defaults to "never"; must be in {never, on_failure, always}
   - no two templates share the same path or the same tag
   - no two instances share the same name
   ```

4. Implement helper `(m *Manifest) ResolveTemplateRef(ref string) (resolved string, kind string)` returning either `("compose:<project>:<tag>", "tag")` or `("sha256-...", "hash")`. Caller uses `kind` to decide which API call to make.

5. Tests cover every validation rule (one positive case, one negative case per rule). Assert multi-error: a manifest with three errors reports all three.

6. **Verify:** `go test ./core/cli/compose/... -run TestManifest -count=1 -race` passes.

---

### Task 16 — Compose state queries (resolver and state)

**Goal:** Resolve template paths to hashes; query the control-api for compose-owned resources.

**Files:** `core/cli/compose/resolver.go`, `core/cli/compose/state.go`, `core/cli/compose/resolver_test.go`, `core/cli/compose/state_test.go`.

1. `core/cli/compose/resolver.go`:

   ```go
   // ResolveTemplate reads a template spec file from disk, applies frame-
   // resolution defaults, and computes its content hash.
   //
   // Reuses core/canonical/jcs.go for canonicalization to match the
   // control-api's hashing exactly.
   func ResolveTemplate(path string) (hash string, specJSON []byte, err error)
   ```

   The function: read YAML → unmarshal to `node.TemplateSpec` → call `node.ApplyFrameResolutionDefaults` → marshal to JSON → call `core/canonical.CanonicalSpecHash` → return.

2. `core/cli/compose/state.go`:

   ```go
   // ComposeState is the slice of control-api state visible to compose
   // for a given project.
   type ComposeState struct {
       Tags      []TagWithTemplate
       Templates []cli.Template       // referenced by any compose-owned tag
       Instances []cli.Instance       // those whose instance_key starts with compose:<project>:
   }

   // QueryState lists the control-api's tags + instances and filters by the
   // project's "compose:<project>:" prefix client-side.
   func QueryState(ctx context.Context, c *cli.Client, project string) (*ComposeState, error)
   ```

3. The `QueryState` function paginates through `ListTags` (cursor-based) and `ListInstances`, filters client-side by the prefix, then `GetTemplate` for each unique `template_id` referenced.

4. Tests against `clitest.NewServer`:
   - State containing tags from two projects → only the requested one returned.
   - Pagination across many tags works.
   - `ResolveTemplate` produces the same hash as `core/canonical.CanonicalSpecHash` would for the same spec.

5. **Verify:** `go test ./core/cli/compose/... -run "TestResolver|TestState" -count=1 -race` passes.

---

### Task 17 — Compose plan computation

**Goal:** Diff manifest against state; produce an ordered list of operations.

**Files:** `core/cli/compose/plan.go`, `core/cli/compose/plan_test.go`.

1. Define plan types. Action and Kind are **string-typed constants** so JSON output emits `"action": "register"` (matching spec §3.2), not `"action": 0`. Every field has a `json:` tag matching the spec's expected schema (snake_case field names: `template_hash`, `instance_key`, etc.):

   ```go
   type Plan struct {
       Project string `json:"project"`
       Context string `json:"context,omitempty"`
       Steps   []Step `json:"plan"`
       Summary Summary `json:"summary"`
   }

   type Summary struct {
       Changes int `json:"changes"`
   }

   type Step struct {
       Action       Action         `json:"action"`
       Kind         Kind           `json:"kind"`
       Tag          string         `json:"tag,omitempty"`
       TemplateHash string         `json:"template_hash,omitempty"`
       TemplateTag  string         `json:"template_tag,omitempty"`
       InstanceID   string         `json:"instance_id,omitempty"`
       InstanceKey  string         `json:"instance_key,omitempty"`
       FromPath     string         `json:"from,omitempty"`
       Params       map[string]any `json:"params,omitempty"`
       Note         string         `json:"note,omitempty"`
   }

   type Action string
   const (
       ActionRegister       Action = "register"
       ActionTagCreate      Action = "tag"          // matches spec §3.2 example
       ActionTagMove        Action = "tag-move"
       ActionDeploy         Action = "deploy"
       ActionInstanceDelete Action = "instance-delete"
       ActionUndeploy       Action = "undeploy"
       ActionTagDelete      Action = "tag-delete"
       ActionInstanceCreate Action = "create"        // matches spec §3.2 example
       ActionTemplateDelete Action = "template-delete"
   )

   type Kind string
   const (
       KindTemplate Kind = "template"
       KindTag      Kind = "tag"
       KindInstance Kind = "instance"
   )
   ```

   Note: spec §3.2's JSON example shows action strings `"register"`, `"tag"`, `"deploy"`, `"create"`. The constants above match those. The other action strings (`tag-move`, `instance-delete`, etc.) extend the vocabulary for the steps spec §3.2 doesn't enumerate.

2. Implement `ComputePlan(ctx, c *cli.Client, m *Manifest, state *ComposeState) (*Plan, error)`. Walk the spec §3.1 diff rules. Then sort the resulting list into the §3.3 step ordering (1. registers, 2. tag creates/moves, 3. deploys, 4. instance deletes, 5. template undeploys, 6. tag deletes, 7. instance creates, 8. template deletes).

3. Errors: a non-terminal compose-owned instance not in the manifest produces an `*ErrComposePlan` with a list of offending instances; the caller surfaces this as exit 1.

4. Restart-policy logic in plan computation:
   - For each manifest instance, look up the existing row (by `compose:<project>:<name>`).
   - If non-existent or only a terminal row exists → schedule create (preceded by delete if a terminal row exists, to free the unique-key slot).
   - If existing non-terminal → leave alone; warn on params drift.
   - If existing terminal: classify success vs failure (use `aggregate_outcome` if `GetInstance` returns it; otherwise call `ListInstanceNodes` and check `state` of each — any `failed` → failure, all `fresh` → success). Apply the policy table from spec §3.5.

5. Tests in `core/cli/compose/plan_test.go` exercise:
   - Empty manifest, empty state → empty plan.
   - Add: 1 template + 1 instance from scratch.
   - Tag-mv: hash changes for an existing tag → register-new + tag-move + undeploy-old + delete-old (sequenced correctly).
   - Remove-from-manifest: deployed template + tag → undeploy + tag-delete + delete (sequenced correctly).
   - Restart=on_failure with terminal failure → delete + create.
   - Restart=on_failure with terminal success → delete only.
   - Restart=never with any terminal → delete only.
   - Non-terminal instance not in manifest → error.
   - Two compose projects share a template hash → tag-delete on one project does not delete the underlying template (DELETE returns 409 → cleanup-skip).

6. **Verify:** `go test ./core/cli/compose/... -run TestPlan -count=1 -race` passes.

---

### Task 18 — Compose apply (`compose up`)

**Goal:** Execute a plan serially; fail-fast on first error; produce both human and JSON output for `compose plan`.

**Files:** `core/cli/compose/apply.go`, `core/cli/compose/apply_test.go`.

1. Implement `ApplyPlan(ctx context.Context, c *cli.Client, plan *Plan, opts ApplyOpts) error`. `ApplyOpts` carries `Yes bool`, `Logger io.Writer`. The function loops over `plan.Steps` and executes each via the appropriate client call. On error, returns immediately wrapping the failed step.

2. Implement `EmitPlan(w io.Writer, plan *Plan, format cli.Format)` for `compose plan` output. Per spec §3.2:
   - Human form: grouped by kind (Templates, Instances), one line per step, action symbols `+ / - / ~`, prefixed-form names everywhere.
   - JSON form: `{project, context, plan: [...], summary: {changes: N}}`.

3. Implement `RunComposeUp(ctx, args) int`:
   - Parse flags (`-f <manifest>`, `--yes`, `--endpoint`, `-o`, `--no-color`).
   - Load + validate manifest.
   - Resolve endpoint (manifest `context` overrides everything else if set).
   - Query state.
   - Compute plan.
   - **Destructive-op pre-check (per spec §3.6).** A plan step is destructive if it falls into any of these categories:
     1. `instance-delete` of a terminal instance whose aggregate outcome was failure (use the same outcome classification as Task 17 step 4).
     2. `undeploy` of a template that has any **active** (non-compose-owned, non-terminal) instances bound to it. Pre-check by calling `ListInstances` filtered by `template_hash` and looking for non-terminal rows whose `instance_key` does not start with `compose:<project>:`. Without this pre-check, the underlying `POST /undeploy` returns a bare 409; the CLI surfaces a clearer "template has active non-compose instances bound" message.
     3. (Implicit on `compose down` only — handled in Task 19.) Any operation triggered by `compose down`.
     4. (Implicit on `dev down --infra` only — handled in Task 20.) Any operation that runs `infra.down.command`.
     If any destructive step is present and not `--yes` and stdin is not a TTY → exit 2 with "destructive operation requires --yes" listing each destructive step. If interactive TTY, prompt `Proceed? [y/N]` and abort on anything other than `y` / `Y`.
     Non-destructive operations (registering, deploying, creating instances, `instance-delete` of successfully-terminal instances during recreate per spec §3.6 last paragraph) never prompt.
   - Apply plan.
   - Print summary (or "no changes").
   - Exit codes: 0 success; 1 runtime/control-api failure; 2 usage; never 3 (3 is `compose plan` only).

4. Implement `RunComposePlan(ctx, args) int`:
   - Same flow up through compute plan.
   - `EmitPlan` and exit. 0 if plan empty; 3 if non-empty; 1 on control-api failure; 2 on local validation failure.

5. Implement `RunComposeStatus(ctx, args) int` per spec §3.8: list + annotate; exit 0 on success, 1 on control-api 5xx, 2 on local validation failure.

6. Tests:
   - End-to-end `compose up` against `clitest.NewServer` from a manifest with one template + one instance.
   - Failure mid-plan: inject a 5xx on `POST /tags`; verify earlier steps committed and apply returned with the failed step. Re-run resumes (idempotent operations).
   - `compose plan` exit code 3 when there's drift; 0 when clean.
   - `compose status` annotates correctly.
   - `compose up` against a manifest with a non-terminal compose-owned-not-in-manifest instance → exit 1 before any API mutation.
   - Destructive-op confirmation: `--yes` works; non-TTY without `--yes` exits 2.

7. **Verify:** `go test ./core/cli/compose/... -run "TestApply|TestComposeUp|TestComposePlan|TestComposeStatus" -count=1 -race` passes.

---

### Task 19 — Compose down

**Goal:** Reverse the manifest.

**Files:** `core/cli/compose/down.go`, `core/cli/compose/down_test.go`.

1. Implement `ComputeDownPlan(ctx, c, m, state) (*Plan, error)` that produces the §3.7 sequence: instance deletes → template undeploys → tag deletes → best-effort template deletes. Refuses with `*ErrComposePlan` if any non-terminal compose-owned instances exist.

2. Implement `RunComposeDown(ctx, args) int`:
   - Parse flags (`-f`, `--yes`, `--infra`, `--endpoint`, `-o`).
   - Load manifest.
   - Resolve endpoint.
   - Compute down plan.
   - Confirm destructive operations.
   - Apply plan (best-effort: 409 from `DELETE /templates/{hash}` is treated as success; 409 from `POST /undeploy` likewise treated as cleanup-skip per spec §3.3 step 5).
   - If `--infra` set and `infra.down.command` defined, run it last via `os/exec`. Non-zero exit → exit 1.

3. Dispatch is wired in Task 21 (`compose.Dispatch` covers all `compose <up|down|plan|status>` verbs in one place). Nothing to add to `main.go` from this task.

4. Tests in `core/cli/compose/down_test.go`:
   - Down against a manifest with one terminal instance: instance delete → undeploy → tag delete → template delete.
   - Down against a non-terminal instance: exit 1, no API mutation.
   - `--infra` runs the down command (assert via a stub command that touches a tempfile).
   - Default `compose down` does NOT run `--infra` even when defined (assert tempfile not touched).

5. **Verify:** `go test ./core/cli/compose/... -run "TestComposeDown" -count=1 -race` passes.

---

### Task 20 — Dev wrapper

**Goal:** `dev up` materializes inline `rimsky_config`, runs `infra.up.command`, polls `wait_for`, runs `compose up`. `dev down --infra` chains `compose down` + `infra.down.command`.

**Files:** `core/cli/compose/dev.go`, `core/cli/compose/dev_test.go`.

1. Implement `MaterializeRimskyConfig(m *Manifest, targetDir string) (path string, err error)`:
   - If `m.RimskyConfig.Inline` is set, marshal to YAML and write to `<targetDir>/.rimsky/rimsky.yml`. **Always overwrite.**
   - If `m.RimskyConfig.Path` is set, return the absolute path of the referenced file (no copy).
   - If neither, return `("", nil)`.

2. Implement `RunInfraUp(ctx, infra *Infra, manifestDir string) error`:
   - Run `infra.up.command` via `exec.CommandContext`. Inherit stdout/stderr. Working directory: `manifestDir`. Environment: inherit + `RIMSKY_PROJECT=<project>`.
   - Wait for it to return; non-zero exit → return error.
   - If `infra.up.wait_for` set, poll the URL with `GET` at 1s intervals until 2xx or `infra.up.timeout` elapses (default 60s).

3. Implement `RunInfraDown(ctx, infra *Infra, manifestDir string) error` analogously (no wait_for).

4. Implement `RunDevUp(ctx, args) int`:
   1. Parse flags (`-f`, `--endpoint`, `-o`, `--yes`).
   2. Load + validate manifest.
   3. Materialize inline rimsky_config if present.
   4. If `infra.up` set, run it. On error → exit 1.
   5. Run `compose up` (delegate to `RunComposeUp` against the same manifest).

5. Implement `RunDevDown(ctx, args) int`:
   1. Parse flags (`-f`, `--yes`, `--infra`, `--endpoint`, `-o`).
   2. Load manifest.
   3. Run `compose down` (delegate to `RunComposeDown` minus the `--infra` flag).
   4. If `--infra` set and `infra.down` defined, run `RunInfraDown`. Non-zero → exit 1.

6. Implement `RunDevStatus(ctx, args) int` as a thin wrapper around `RunComposeStatus`.

7. Tests in `core/cli/compose/dev_test.go`:
   - Materialization writes the file; re-run overwrites.
   - `dev up` with a stub `infra.up.command` (e.g. `["true"]`) and `wait_for` pointing at the test server's `/health` succeeds.
   - `dev up` with `infra.up.command: ["false"]` exits 1 before running compose.
   - `dev up` with `wait_for` URL that never returns 2xx → exits 1 after timeout.

8. **Verify:** `go test ./core/cli/compose/... -run "TestMaterialize|TestRunInfra|TestDev" -count=1 -race` passes.

---

### Task 21 — Compose subcommand dispatcher

**Goal:** Wire `compose` and `dev` subgroups into `main.go`.

**Files:** `core/cmd/rimsky-cli/main.go`, `core/cli/compose/cmd.go`.

1. Create `core/cli/compose/cmd.go` exposing `Dispatch(ctx, args) int` for `compose <up|down|plan|status>` and `DispatchDev(ctx, args) int` for `dev <up|down|status>`.

2. In `main.go`, add:

   ```go
   case "compose":
       os.Exit(compose.Dispatch(context.Background(), os.Args[2:]))
   case "dev":
       os.Exit(compose.DispatchDev(context.Background(), os.Args[2:]))
   ```

3. **Verify:** `go build ./core/cmd/rimsky-cli/` succeeds. `make cli && ./bin/rimsky-cli compose plan -f /nonexistent.yml; echo $?` prints `2`.

---

### Task 22 — Final main.go assembly + root help

**Goal:** All verbs reachable from `rimsky-cli --help`. Help text matches the spec's verb inventory in §1.2.

**Files:** `core/cmd/rimsky-cli/main.go`.

1. Replace `printRootUsage` with a verbose listing grouped by category:

   ```
   rimsky-cli — orchestration CLI for the rimsky platform.

   Dev-loop:
     run <file>            Register, deploy, instantiate in one shot
     register <file>
     deploy <ref>
     undeploy <ref>
     instantiate <ref>
     rm-instance <id>      Delete a terminal instance
     ls [templates|instances|tags]
     logs <id-or-key>      Stream events (poll-based)
     health
     init [<dir>]          Scaffold a starter project

   Compose:
     compose up | down | plan | status
     dev up | down | status

   Literal API:
     template register | list | get | deploy | undeploy | rm
     tag create | list | get | mv | rm
     instance create | list | get | delete | nodes | events
     node get
     admin force-fire | invalidate | reset

   Context:
     ctx list | use | add | rm | current

   Common flags (all verbs):
     --endpoint <url>     Override control-api endpoint
     -o human|json        Output format (default human)
     --no-color           Disable ANSI color
     --yes                Confirm destructive operations
     -h, --help           Show this help
   ```

2. Verify the dispatcher covers every verb listed.

3. **Verify:**

   ```sh
   make cli
   ./bin/rimsky-cli help | grep -q "rimsky-cli — orchestration CLI"
   # Every top-level verb returns 0 or 2 with --help (no panic, no exit 1, and
   # never silently exit 0 with no output). Exit 1 is reserved for runtime
   # errors which --help should never produce.
   FAIL=0
   for verb in run register deploy undeploy instantiate rm-instance ls logs health init \
               compose dev template tag instance node admin ctx version; do
     output=$(./bin/rimsky-cli $verb --help 2>&1)
     ec=$?
     if [ $ec -ne 0 ] && [ $ec -ne 2 ]; then
       echo "FAIL: $verb --help exited $ec"; FAIL=1
     elif [ -z "$output" ]; then
       echo "FAIL: $verb --help printed nothing"; FAIL=1
     fi
   done
   exit $FAIL
   ```

   The verify passes if the script exits 0.

---

### Task 23 — End-to-end smoke test

**Goal:** Real `docker-compose.yml` stack + real CLI exercising the dev loop.

**Files:** `test/smoke/cli/smoke_test.go`.

1. Create `test/smoke/cli/smoke_test.go` (build-tagged `//go:build smoke` so it doesn't run with the default `go test ./...`):

   ```go
   //go:build smoke

   package cli_smoke

   import "testing"

   func TestCLISmoke(t *testing.T) {
       // 1. Find the rimsky-cli binary (built by `make cli`); skip if absent.
       // 2. mkdir tempdir; cd tempdir.
       // 3. Run `rimsky-cli init .`. Assert files created.
       // 4. Run `rimsky-cli dev up -f rimsky-compose.yml`. Assert exit 0
       //    within 90s (docker compose pull + start can be slow).
       // 5. Poll `rimsky-cli ls` until the example instance appears.
       // 6. Poll until the instance reaches terminal state (15s timeout —
       //    the example graph is one stub-mode HTTP-node, should be fast).
       // 7. Run `rimsky-cli compose status`. Assert it runs cleanly.
       // 8. Run `rimsky-cli compose down --infra --yes`. Assert exit 0.
       // 9. Run `rimsky-cli compose status`. Assert no compose-owned resources.
   }
   ```

2. Reuse `test/smoke/setup.go`'s helpers if any apply; otherwise inline the docker-compose lifecycle.

3. Add a Makefile target:

   ```makefile
   .PHONY: smoke-cli
   smoke-cli: cli
   	go test -tags smoke -count=1 -timeout 5m ./test/smoke/cli/...
   ```

4. **Verify:** `make smoke-cli` passes if Docker is available. (Skip gracefully with `t.Skip` if Docker is not detected — check via `docker info` exit code.)

---

### Task 24 — CLI Docker image

**Goal:** Distroless container image for CI use.

**Files:** `Dockerfile.cli`, `Makefile`.

1. Create `Dockerfile.cli`:

   ```dockerfile
   FROM golang:1.22 AS builder
   WORKDIR /src
   COPY go.mod go.sum ./
   RUN go mod download
   COPY . .
   ARG VERSION=dev
   RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/rimsky-cli ./core/cmd/rimsky-cli/

   FROM gcr.io/distroless/static:nonroot
   COPY --from=builder /out/rimsky-cli /usr/local/bin/rimsky-cli
   ENTRYPOINT ["/usr/local/bin/rimsky-cli"]
   ```

2. Add a Makefile target:

   ```makefile
   .PHONY: cli-image
   cli-image:
   	docker build -f Dockerfile.cli --build-arg VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) -t rimsky/cli:latest .
   ```

3. **Verify:** `make cli-image` builds successfully, then `docker run --rm rimsky/cli:latest version` prints the version. Skip the verify step if Docker isn't available (don't error out).

---

### Task 25 — Documentation updates

**Goal:** Operator guide, architecture doc, glossary, CLAUDE.md, CHANGELOG entries.

**Files:** `docs/operator-guide.md`, `docs/architecture.md`, `docs/glossary.md`, `CLAUDE.md`, `CHANGELOG.md`.

1. **`docs/operator-guide.md`** — add five new sections, ordered after the existing "Deployment" section (around current §2):

   - **§N: Installing the CLI.** Channels per spec §6.1 (Homebrew, install script, GitHub Releases, `go install`, `rimsky/cli` Docker image). Reference URLs are placeholders (`https://rimsky.io/install.sh`, `fallguy/rimsky/rimsky` Homebrew tap) — note that publication of these channels is operator-responsibility outside the spec; the binary ships from this repo and is consumable by all of them.
   - **§N+1: Using the CLI (dev loop).** Walkthrough: `rimsky-cli init myproject && cd myproject && rimsky-cli dev up && rimsky-cli ls && rimsky-cli logs <instance-id>`.
   - **§N+2: Compose manifests.** Full `rimsky-compose.yml` reference using §2 of the spec verbatim, with examples.
   - **§N+3: Contexts.** `~/.rimsky/config.yml` shape, the `ctx` verbs, flag/env/file precedence.
   - **§N+4: Cloud deployment workflows.** Reference recipe: deploy rimsky to k8s/Terraform/etc. via your own IaC; install the CLI on the operator workstation; configure a context pointing at the cloud control-api; use `compose up` (without `dev up`) for application-layer reconciliation. Note that `infra:` is omitted in cloud manifests, or its commands invoke `terraform apply` / `kubectl apply` / etc.

2. **`docs/architecture.md`** — add a paragraph under the existing package layout that introduces `core/cmd/rimsky-cli/` and `core/cli/`. State that the CLI is a thin client over the control-api with no orchestration logic; link to the spec.

3. **`docs/glossary.md`** — add definitions for "compose project", "compose manifest", "context", "infra (operator-supplied)". Per spec §10, do **not** modify the existing "substrate" entry in the auth doc (out of scope here).

4. **`CLAUDE.md`** — append two gotcha entries under the existing "Non-obvious gotchas" section:

   - *"Compose owns project-prefixed names."* Tags `compose:<project>:<...>` and instance keys `compose:<project>:<...>` are reserved for `rimsky-cli compose`. Manual `rimsky-cli template register --tag compose:foo:bar` is rejected by the CLI; manual API calls bypass that check and would conflict with compose ownership.
   - *"`rimsky-cli` is a thin client; v1 does not version the control-api."* The CLI talks to bare paths (no `/v1/` prefix) and does not check server version; rolling upgrades are operator-managed.

5. **`CHANGELOG.md`** — add an Unreleased bullet:

   ```
   - Add `rimsky-cli` and `rimsky-compose.yml` for operator-facing
     control-api orchestration. Per
     `docs/specs/2026-05-02-rimsky-cli-and-compose-design.md`.
   ```

6. **Verify:** `git diff docs/ CLAUDE.md CHANGELOG.md` shows the expected additions; no other doc files modified. No grep for "TODO" / "FIXME" in the new sections (`grep -nE 'TODO|FIXME' docs/operator-guide.md` empty for the new sections).

---

### Task 26 — Full build + test sweep

**Goal:** Confirm the whole repo still builds and tests pass.

1. `go build ./...` — exit 0.
2. `go vet ./...` — exit 0.
3. `make lint` — exit 0.
4. `go test ./... -count=1 -race` — exit 0.
5. `make cli` — produces `bin/rimsky-cli`.
6. `./bin/rimsky-cli help` — exit 0; help text matches Task 22's expected layout.
7. `./bin/rimsky-cli health` against an unreachable endpoint — exit 1.

If any of these fails, fix the root cause and retry. Do not skip tests or `make lint` to "deal with later."

**Verify:** All seven commands above exit 0 (or 1 in the last case as documented). Capture the output of `go test ./... -count=1 -race | tail -30` to confirm the new test packages all show `ok`.

---

## Manual checks after completion

These are not part of the automated run; the user (or a follow-up reviewer) confirms them after the plan finishes.

1. **Smoke test against the live stack.** With Docker Desktop running: `make smoke-cli`. Expected: pass within 5 minutes. Catches integration issues that the in-process fake doesn't.
2. **`init` UX walkthrough.** In a fresh tempdir: `rimsky-cli init && rimsky-cli dev up && rimsky-cli ls && rimsky-cli compose down --infra --yes`. Confirm the user-facing output is readable and not surprising.
3. **Operator-guide review.** Read the new sections in `docs/operator-guide.md` cold; confirm a first-time user could install and run the CLI from them.
